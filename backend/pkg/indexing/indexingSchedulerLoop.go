package indexing

import (
	"sort"
	"time"

	"github.com/gtsteffaniak/go-logger/logger"
)

// alignCeilToInterval rounds t up (UTC) to the next boundary where Unix time is a multiple of interval.
func alignCeilToInterval(t time.Time, interval time.Duration) time.Time {
	if interval < time.Minute {
		interval = time.Minute
	}
	stepNs := interval.Nanoseconds()
	total := t.UTC().UnixNano()
	aligned := (total + stepNs - 1) / stepNs * stepNs
	return time.Unix(0, aligned).UTC()
}

// computeNextSlotTime returns the next scheduled slot after a scan completes.
// interval must be one of scanScheduleTiers; alignment uses that interval as the grid step.
func computeNextSlotTime(lastScanned, ref time.Time, interval time.Duration) time.Time {
	minStep := scanScheduleTiers[0]
	if interval < minStep {
		interval = minStep
	}
	minNext := lastScanned.Add(interval)
	if minNext.Before(ref) {
		minNext = ref
	}
	return alignCeilToInterval(minNext, interval)
}

func (idx *Index) removeScannerFromSlotLocked(s *Scanner) {
	sec := s.calendarSlotSec
	if sec == 0 {
		return
	}
	if idx.scheduleSlots == nil {
		s.calendarSlotSec = 0
		return
	}
	lst := idx.scheduleSlots[sec]
	out := lst[:0]
	for _, x := range lst {
		if x != s {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		delete(idx.scheduleSlots, sec)
	} else {
		idx.scheduleSlots[sec] = out
	}
	s.calendarSlotSec = 0
}

// registerScannerNextRun removes the scanner from any previous slot and adds it at the given UTC time.
func (idx *Index) registerScannerNextRun(s *Scanner, when time.Time) {
	when = when.UTC().Truncate(time.Second)
	sec := when.Unix()
	idx.scheduleSlotsMu.Lock()
	defer idx.scheduleSlotsMu.Unlock()
	if idx.scheduleSlots == nil {
		idx.scheduleSlots = make(map[int64][]*Scanner)
	}
	idx.removeScannerFromSlotLocked(s)
	idx.scheduleSlots[sec] = append(idx.scheduleSlots[sec], s)
	s.calendarSlotSec = sec
}

func (idx *Index) takeDueScannerBatch(now time.Time) []*Scanner {
	idx.scheduleSlotsMu.Lock()
	defer idx.scheduleSlotsMu.Unlock()
	if idx.scheduleSlots == nil {
		return nil
	}
	var dueSec int64 = -1
	nowSec := now.Unix()
	for sec := range idx.scheduleSlots {
		if sec <= nowSec {
			if dueSec < 0 || sec < dueSec {
				dueSec = sec
			}
		}
	}
	if dueSec < 0 {
		return nil
	}
	lst := idx.scheduleSlots[dueSec]
	delete(idx.scheduleSlots, dueSec)
	for _, s := range lst {
		s.calendarSlotSec = 0
	}
	return lst
}

func (idx *Index) earliestSlotWake(now time.Time) time.Time {
	idx.scheduleSlotsMu.Lock()
	defer idx.scheduleSlotsMu.Unlock()
	if len(idx.scheduleSlots) == 0 {
		return now.Add(time.Minute)
	}
	nowSec := now.Unix()
	var earliest int64 = -1
	for sec := range idx.scheduleSlots {
		if sec <= nowSec {
			return now
		}
		if earliest < 0 || sec < earliest {
			earliest = sec
		}
	}
	if earliest < 0 {
		return now.Add(time.Minute)
	}
	return time.Unix(earliest, 0).UTC()
}

func (idx *Index) restoreScannerNextRuns() {
	idx.scheduleSlotsMu.Lock()
	idx.scheduleSlots = make(map[int64][]*Scanner)
	idx.scheduleSlotsMu.Unlock()

	idx.mu.RLock()
	n := len(idx.scanners)
	idx.mu.RUnlock()
	now := time.Now()
	for _, s := range idx.scanners {
		var next time.Time
		s.withStatsLock(func() {
			if s.lastScanned.IsZero() {
				s.nextRun = time.Time{}
				return
			}
			next = idx.nextScanTime(s.lastScanned, now, s.currentSchedule)
			s.nextRun = next
		})
		if !next.IsZero() {
			idx.registerScannerNextRun(s, next)
		}
	}
	logger.Debugf("[%s] scheduler: restored nextRun for %d scanners (interval=%s, fullScanEvery=%d)", idx.Name, n, idx.scanScheduleConfig().ScanInterval, idx.fullScanEvery())
}

func sortedScannerPaths(idx *Index) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	paths := make([]string, 0, len(idx.scanners))
	for p := range idx.scanners {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func (idx *Index) pruneGoneChildScanners() {
	idx.mu.RLock()
	var gone []string
	for path, s := range idx.scanners {
		if path == "/" {
			continue
		}
		if !s.directoryExists() {
			gone = append(gone, path)
		}
	}
	idx.mu.RUnlock()

	for _, path := range gone {
		idx.mu.RLock()
		s, ok := idx.scanners[path]
		idx.mu.RUnlock()
		if !ok {
			continue
		}
		if idx.db != nil {
			if err := s.removeScannedDirectory(path); err != nil {
				logger.Warningf("remove missing scanner %s: %v", path, err)
				continue
			}
		} else {
			s.removeSelf()
		}

		logger.Debugf("Scheduler removed scanner [%s]: path gone", path)
	}
}

func (idx *Index) needsAdaptiveBootstrap() bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for _, s := range idx.scanners {
		s.statsMu.RLock()
		unscanned := s.lastScanned.IsZero()
		s.statsMu.RUnlock()
		if unscanned {
			return true
		}
	}
	return false
}

func (idx *Index) runSerialScanPass() {
	idx.pruneGoneChildScanners()
	paths := sortedScannerPaths(idx)
	idx.mu.Lock()
	idx.schedulerBatch++
	idx.mu.Unlock()
	defer func() {
		idx.mu.Lock()
		idx.schedulerBatch--
		idx.mu.Unlock()
		if err := idx.PostScan(); err != nil {
			logger.Errorf("PostScan after serial pass: %v", err)
		}
	}()

	for _, p := range paths {
		if idx.isStopped() {
			return
		}
		idx.mu.RLock()
		s, ok := idx.scanners[p]
		idx.mu.RUnlock()
		if !ok {
			continue
		}
		s.statsMu.RLock()
		unscanned := s.lastScanned.IsZero()
		s.statsMu.RUnlock()
		if !unscanned {
			continue // discovering a new child must not restart existing scanners early
		}
		s.executeScan()
	}
}

func (idx *Index) schedulerAdaptiveTick() {
	idx.pruneGoneChildScanners()
	if idx.needsAdaptiveBootstrap() {
		logger.Debugf("[%s] scheduler: bootstrap unscanned paths", idx.Name)
		idx.runSerialScanPass()
		return
	}
	drainedAny := false
	for {
		batch := idx.takeDueScannerBatch(time.Now())
		if len(batch) == 0 {
			break
		}
		drainedAny = true
		sort.Slice(batch, func(i, j int) bool {
			return batch[i].scanPath < batch[j].scanPath
		})
		firstPath := batch[0].scanPath
		logger.Debugf("[%s] scheduler: slot scanners=%d first=%s", idx.Name, len(batch), firstPath)
		for _, s := range batch {
			if idx.isStopped() {
				return
			}
			var nextRunStr string
			s.withStatsRLock(func() {
				if s.nextRun.IsZero() {
					nextRunStr = "zero(immediate)"
				} else {
					nextRunStr = s.nextRun.UTC().Format(time.RFC3339)
				}
			})
			logger.Debugf("[%s] scheduler: run path=%s nextRun=%s", idx.Name, s.scanPath, nextRunStr)
			s.executeScan()
		}
	}
	if !drainedAny {
		idx.mu.RLock()
		n := len(idx.scanners)
		idx.mu.RUnlock()
		now := time.Now()
		logger.Debugf("[%s] scheduler: tick no due slot (now=%v scanners=%d)", idx.Name, now.UTC().Format(time.RFC3339), n)
	}
}

func (idx *Index) runIndexScheduler() {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("[%s] index scheduler panic recovered: %v", idx.Name, r)
		}
	}()

	idx.mu.RLock()
	n := len(idx.scanners)
	idx.mu.RUnlock()
	logger.Debugf("[%s] scheduler: started scanners=%d interval=%s fullScanEvery=%d", idx.Name, n, idx.scanScheduleConfig().ScanInterval, idx.fullScanEvery())
	idx.schedulerAdaptiveTick()
	for {
		now := time.Now()
		next := idx.earliestSlotWake(now)
		d := time.Until(next).Truncate(time.Second)
		if d < time.Second {
			d = time.Second
		}
		logger.Debugf("[%s] scheduler: sleep until=%v (in %v)", idx.Name, next.UTC().Format(time.RFC3339), d)
		select {
		case <-idx.schedulerStop:
			logger.Debugf("[%s] scheduler: loop stopped", idx.Name)
			return
		case <-time.After(d):
			idx.schedulerAdaptiveTick()
		}
	}
}

func (idx *Index) stopScheduler() {
	idx.mu.Lock()
	idx.stopRequested = true
	stop := idx.schedulerStop
	idx.mu.Unlock()
	if stop != nil {
		idx.schedulerStopOnce.Do(func() { close(stop) })
	}
}

func (idx *Index) isStopped() bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.stopRequested
}
