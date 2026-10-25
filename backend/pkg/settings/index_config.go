package settings

import "encoding/json"

// Connection credentials are configuration input, never API response data.
func (c IndexSqlConfig) MarshalJSON() ([]byte, error) {
	type publicIndexConfig IndexSqlConfig
	safe := publicIndexConfig(c)
	safe.DSN = ""
	return json.Marshal(safe)
}
