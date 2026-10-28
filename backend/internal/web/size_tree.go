package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	dbsql "github.com/gtsteffaniak/filebrowser/backend/internal/database/sql"
	"github.com/gtsteffaniak/filebrowser/backend/internal/state"
	"github.com/gtsteffaniak/filebrowser/backend/internal/utils"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/indexing"
	"github.com/gtsteffaniak/filebrowser/backend/pkg/indexing/iteminfo"
)

type sizeTreeResponse struct {
	Source          string               `json:"source"`
	Root            *dbsql.SizeTreeNode  `json:"root"`
	Status          indexing.IndexStatus `json:"status"`
	LastIndexedUnix int64                `json:"lastIndexedUnixTime"`
	NodeLimit       int                  `json:"nodeLimit"`
	ChildrenLimit   int                  `json:"childrenLimit"`
}

// sizeTreeHandler returns indexed children only; it never resolves paths on disk.
// @Summary Read an indexed directory size map
// @Tags Tools
// @Produce json
// @Param source query string true "Source name"
// @Param path query string false "Directory relative to the user's source scope"
// @Param depth query int false "One or two levels; default two"
// @Param limit query int false "Maximum direct children per folder, largest first" default(80) minimum(1) maximum(1000)
// @Success 200 {object} sizeTreeResponse
// @Failure 400 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /api/tools/size-tree [get]
func sizeTreeHandler(w http.ResponseWriter, r *http.Request, d *Context) (int, error) {
	name := r.URL.Query().Get("source")
	idx := indexing.GetIndex(name)
	if idx == nil {
		return http.StatusNotFound, fmt.Errorf("source not found")
	}
	scope, err := d.User.GetScopeForSourcePath(idx.Path)
	perms, hasPerms := d.User.FilePermsForSourcePath(idx.Path)
	if err != nil || !hasPerms || !perms.View {
		return http.StatusForbidden, fmt.Errorf("source is not viewable")
	}
	rel := r.URL.Query().Get("path")
	if rel == "" {
		rel = "/"
	}
	// Reject traversal rather than normalizing an attempted escape into root.
	for _, part := range strings.Split(strings.ReplaceAll(rel, `\`, "/"), "/") {
		if part == ".." {
			return http.StatusBadRequest, fmt.Errorf("invalid directory path")
		}
	}
	rel, err = utils.SanitizePath(rel)
	if err != nil {
		return http.StatusBadRequest, err
	}
	scope = utils.AddTrailingSlashIfNotExists(path.Join("/", scope))
	indexPath := utils.AddTrailingSlashIfNotExists(path.Join(scope, rel))
	if !strings.HasPrefix(indexPath, scope) || !state.AccessPermitted(idx.Path, utils.IndexPathFromNormalized(indexPath, true), d.User.Username) {
		return http.StatusForbidden, fmt.Errorf("directory is not accessible")
	}
	depth := 2
	if raw := r.URL.Query().Get("depth"); raw != "" {
		depth, err = strconv.Atoi(raw)
		if err != nil || depth < 1 || depth > 2 {
			return http.StatusBadRequest, fmt.Errorf("depth must be 1 or 2")
		}
	}
	childrenLimit := dbsql.SizeTreeDefaultChildrenLimit
	if r.URL.Query().Has("limit") {
		childrenLimit, err = strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil || childrenLimit < 1 || childrenLimit > dbsql.SizeTreeMaxChildrenLimit {
			return http.StatusBadRequest, fmt.Errorf("limit must be an integer between 1 and %d", dbsql.SizeTreeMaxChildrenLimit)
		}
	}
	allowed := func(item *iteminfo.FileInfo) bool {
		if !strings.HasPrefix(item.Path, scope) {
			return false
		}
		if item.Hidden && !d.User.ShowHidden {
			return false
		}
		if !item.IsDir && item.Type != "directory" && utils.HideFileByExt(item.Name, d.User.HideFileExt) {
			return false
		}
		return state.AccessPermitted(idx.Path, utils.IndexPathFromNormalized(item.Path, item.IsDir || item.Type == "directory"), d.User.Username)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	root, err := idx.ReadSizeTree(ctx, indexPath, depth, childrenLimit, allowed)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return http.StatusNotFound, fmt.Errorf("directory has not been indexed yet")
		}
		if errors.Is(err, indexing.ErrIndexNotReady) {
			return http.StatusServiceUnavailable, err
		}
		return http.StatusServiceUnavailable, fmt.Errorf("directory index is busy or unavailable; please retry")
	}
	var trimScope func(*dbsql.SizeTreeNode)
	trimScope = func(node *dbsql.SizeTreeNode) {
		node.Path = "/" + strings.TrimPrefix(node.Path, scope)
		if node.Path == "/" {
			node.Name = name
		}
		for _, child := range node.Children {
			trimScope(child)
		}
	}
	trimScope(root)
	last := idx.GetLastIndexed()
	var lastUnix int64
	if !last.IsZero() {
		lastUnix = last.Unix()
	}
	return RenderJSON(w, r, sizeTreeResponse{Source: name, Root: root, Status: idx.GetStatus(), LastIndexedUnix: lastUnix, NodeLimit: dbsql.SizeTreeNodeBudget(childrenLimit), ChildrenLimit: childrenLimit})
}
