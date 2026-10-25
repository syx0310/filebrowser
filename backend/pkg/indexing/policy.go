package indexing

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gtsteffaniak/filebrowser/backend/pkg/settings"
)

func (idx *Index) sizeOnly() bool { return settings.Config.Server.IndexSqlConfig.Mode == "size" }

func (idx *Index) includeHidden() bool { return settings.Config.Server.IndexSqlConfig.IncludeHidden }

func (idx *Index) excludesHiddenPath(path string, hidden bool) bool {
	if idx.includeHidden() || path == "/" {
		return false
	}
	if hidden {
		return true
	}
	for _, component := range strings.Split(path, "/") {
		if strings.HasPrefix(component, ".") && component != "." && component != ".." {
			return true
		}
	}
	return false
}

// Persist the accounting policy with the index so a changed policy cannot reuse
// stale sizes or leave excluded paths searchable after a restart.
func (idx *Index) policyFingerprint() string {
	policy := struct {
		Path                                 string
		SizeOnly, IncludeHidden, LogicalSize bool
		Rules                                []settings.ConditionalRule
	}{idx.Path, idx.sizeOnly(), idx.includeHidden(), idx.Config.UseLogicalSize, idx.Config.Rules}
	data, _ := json.Marshal(policy)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
