package web

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gtsteffaniak/filebrowser/backend/internal/adapters/fs/fileutils"
	dbsql "github.com/gtsteffaniak/filebrowser/backend/internal/database/sql"
	"github.com/gtsteffaniak/filebrowser/backend/internal/database/users"
	"github.com/gtsteffaniak/filebrowser/backend/internal/state"
	"github.com/gtsteffaniak/filebrowser/backend/internal/utils"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/indexing"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/indexing/iteminfo"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/settings"
)

func TestSizeTreeHandlerScopeAndPermissions(t *testing.T) {
	previous, previousDB := settings.Config, indexing.GetIndexDB()
	t.Cleanup(func() {
		settings.Config = previous
		indexing.SetIndexDBForTesting(previousDB)
		indexing.ClearTestIndices()
	})
	settings.Config = settings.SetDefaults(false)
	setupTestEnv(t)
	settings.Config.Server.CacheDir = t.TempDir()
	fileutils.SetFsPermissions(0644, 0755)
	db, _, err := dbsql.NewIndexDB("size-tree-http", "OFF", 100, 32, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	indexing.SetIndexDBForTesting(db)
	// This source doesn't exist on disk: a successful response proves no filesystem walk is required.
	source := &settings.Source{Name: "tree", Path: filepath.Join(t.TempDir(), "not-mounted")}
	source.Config.ResolvedRules.IndexingDisabled = true
	settings.Config.Server.NameToSource[source.Name] = source
	settings.Config.Server.SourceMap[source.Path] = source
	indexing.Initialize(source, true, false)
	user := &users.User{FrontendUser: users.FrontendUser{Username: "size-user"}, BackendScopes: []users.BackendScope{{Path: source.Path, Scope: "/tenant", Permissions: users.SourceFilePermissions{View: true}}}}
	item := func(path string, dir bool, hidden bool) *iteminfo.FileInfo {
		typ := "blob"
		if dir {
			typ = "directory"
		}
		parts := strings.Split(strings.Trim(path, "/"), "/")
		return &iteminfo.FileInfo{Path: path, ItemInfo: iteminfo.ItemInfo{Name: parts[len(parts)-1], Type: typ, Size: 10, Hidden: hidden}}
	}
	items := []*iteminfo.FileInfo{item("/tenant/", true, false), item("/tenant/visible", false, false), item("/tenant/.hidden", false, true), item("/tenant/excluded.tmp", false, false), item("/tenant/denied/", true, false), item("/tenant/denied/private", false, false), item("/outside/", true, false), item("/outside/secret", false, false)}
	if err := db.BulkInsertItems(source.Name, items); err != nil {
		t.Fatal(err)
	}
	if err := state.DenyAll(source.Path, utils.IndexPathFromNormalized("/tenant/denied/", true)); err != nil {
		t.Fatal(err)
	}
	user.HideFileExt = ".tmp"
	request := func(path string) (int, string, error) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		status, err := sizeTreeHandler(w, r, &Context{User: user})
		return status, w.Body.String(), err
	}
	status, body, err := request("/api/tools/size-tree?source=tree&path=/")
	if err != nil {
		t.Fatalf("status=%d err=%v", status, err)
	}
	var got sizeTreeResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Root.Path != "/" || len(got.Root.Children) != 1 || got.Root.Children[0].Path != "/visible" {
		t.Fatalf("wrong scoped result: %s", body)
	}
	if got.ChildrenLimit != dbsql.SizeTreeDefaultChildrenLimit || got.NodeLimit != dbsql.SizeTreeNodeLimit {
		t.Fatalf("unexpected default limits: %+v", got)
	}
	for _, secret := range []string{"tenant", "secret", "denied", "hidden", "excluded"} {
		if strings.Contains(body, secret) {
			t.Fatalf("exposed filtered name %s", secret)
		}
	}
	user.ShowHidden = true
	_, body, err = request("/api/tools/size-tree?source=tree")
	if err != nil || !strings.Contains(body, ".hidden") {
		t.Fatalf("hidden preference ignored: %v", err)
	}
	_, body, err = request("/api/tools/size-tree?source=tree&limit=1")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.ChildrenLimit != 1 || len(got.Root.Children) != 1 || !got.Root.HasMore {
		t.Fatalf("requested limit not applied: %s", body)
	}
	_, body, err = request("/api/tools/size-tree?source=tree&limit=1000")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.ChildrenLimit != 1000 || got.NodeLimit != dbsql.SizeTreeNodeBudget(1000) || len(got.Root.Children) != 2 {
		t.Fatalf("raised limit or access filtering not applied: %s", body)
	}
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"path=../outside", 400}, {"path=%2F..%2Foutside", 400}, {"path=denied", 403}, {"path=missing", 404}, {"depth=3", 400}, {"depth=0", 400},
		{"limit=0", 400}, {"limit=-1", 400}, {"limit=1001", 400}, {"limit=1.5", 400}, {"limit=abc", 400}, {"limit=", 400}, {"limit=999999999999999999999", 400},
	} {
		status, _, _ := request("/api/tools/size-tree?source=tree&" + tc.query)
		if status != tc.status {
			t.Fatalf("%s status=%d want=%d", tc.query, status, tc.status)
		}
	}
	user.BackendScopes[0].Permissions = users.DenyAllSourceFilePermissions()
	if status, _, _ := request("/api/tools/size-tree?source=tree"); status != 403 {
		t.Fatalf("no view permission: %d", status)
	}
	user.BackendScopes = nil
	if status, _, _ := request("/api/tools/size-tree?source=tree"); status != 403 {
		t.Fatalf("no scope: %d", status)
	}
}
