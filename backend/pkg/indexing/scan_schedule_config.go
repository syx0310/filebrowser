package indexing

import (
	"time"

	"github.com/gtsteffaniak/filebrowser/backend/pkg/settings"
)

func (idx *Index) scanScheduleConfig() settings.IndexingConfig {
	return idx.Config.Indexing.Resolve(settings.Config.Server.Indexing)
}

func (idx *Index) fixedScanInterval() time.Duration {
	// Configuration is validated before sources start.
	interval, _ := settings.ParseScanInterval(idx.scanScheduleConfig().ScanInterval)
	return interval
}

func (idx *Index) fullScanEvery() int { return idx.scanScheduleConfig().FullScanEvery }

func (idx *Index) restoreFullScanCounter(counter int) int {
	if counter < 0 || counter >= idx.fullScanEvery() {
		return 0 // a shorter configured cycle is already due for a full scan
	}
	return counter
}

func (idx *Index) nextScanTime(lastScanned, ref time.Time, tier int) time.Time {
	if interval := idx.fixedScanInterval(); interval > 0 {
		next := lastScanned.Add(interval)
		if next.Before(ref) {
			next = ref
		}
		// Calendar entries have second precision; never start before the configured delay.
		if next.Nanosecond() != 0 {
			next = next.Truncate(time.Second).Add(time.Second)
		}
		return next.UTC()
	}
	return computeNextSlotTime(lastScanned, ref, scanScheduleDuration(tier))
}

func unixTimeOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
