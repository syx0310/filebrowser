package settings

import (
	"fmt"
	"strings"
	"time"
)

const DefaultFullScanEvery = 5

// Resolve overlays a source's scheduling choices on the global defaults.
// An explicit "auto" opts a source out of a global fixed interval.
func (c IndexingConfig) Resolve(defaults IndexingConfig) IndexingConfig {
	if strings.TrimSpace(c.ScanInterval) == "" {
		c.ScanInterval = defaults.ScanInterval
	}
	c.ScanInterval = strings.TrimSpace(c.ScanInterval)
	if c.ScanInterval == "" {
		c.ScanInterval = "auto"
	}
	if c.FullScanEvery == 0 {
		c.FullScanEvery = defaults.FullScanEvery
	}
	if c.FullScanEvery == 0 {
		c.FullScanEvery = DefaultFullScanEvery
	}
	return c
}

// ParseScanInterval returns zero for adaptive scheduling. Fixed intervals are
// measured from completion, with no complexity clamp or wall-clock alignment.
func ParseScanInterval(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "auto" {
		return 0, nil
	}
	interval, err := time.ParseDuration(value)
	if err != nil || interval < time.Minute {
		return 0, fmt.Errorf("scanInterval must be auto or a duration of at least 1m (for example 30m, 6h or 24h)")
	}
	return interval, nil
}
