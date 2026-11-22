package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const adaptiveInterval = 2 * time.Second

var adaptiveFPS = [...]int{100, 80, 65, 50, 35}

type videoAdaptation struct {
	level, bad, good int
}

// Two bad observations lower a step. Recovery waits six observations and
// increases one step at a time. Inconclusive intervals never earn a recovery.
func (a *videoAdaptation) observe(subscribers, loss int) {
	if subscribers == 0 {
		*a = videoAdaptation{}
		return
	}
	switch {
	case loss >= 12:
		a.good = 0
		a.bad++
		if a.bad >= 2 || loss >= 35 {
			a.level = min(a.level+1, len(adaptiveFPS)-1)
			a.bad = 0
		}
	case loss >= 0 && loss <= 3:
		a.bad = 0
		a.good++
		if a.good >= 6 {
			a.level = max(0, a.level-1)
			a.good = 0
		}
	default:
		a.bad, a.good = 0, 0
	}
}

func budgetFPS(level, fps int) int {
	return max(1, (fps*adaptiveFPS[level]+99)/100)
}

func budgetJPEG(level, width, limit, quality int, resize bool) (int, int) {
	if level < 2 {
		return limit, quality
	}
	quality = max(min(quality, 55), quality-5*(level-1))
	if level < 3 || !resize {
		return limit, quality
	}
	base := width
	if limit > 0 {
		base = min(base, limit)
	}
	return min(base, max(640, base*(85-17*(level-3))/100)), quality
}

// Reports compare completed JPEGs with progression of the wire frame sequence.
// Only a moving sequence is evidence of attempted frames. Static scenes and
// paused capture therefore cannot be mistaken for a slow connection.
type videoReportWindow struct {
	high, base uint32
	complete   int
	started    bool
}

func (w *videoReportWindow) see(seq uint32) {
	if !w.started {
		w.high, w.base, w.started = seq, seq-1, true
	} else if newer(seq, w.high) {
		w.high = seq
	}
}

func (w *videoReportWindow) report() (expected, complete int) {
	if !w.started {
		return 0, 0
	}
	expected = int(uint32(w.high - w.base))
	if expected > 600 {
		w.base, w.complete = w.high, 0
		return 0, 0
	}
	if expected < 6 {
		return 0, 0
	}
	complete = min(expected, w.complete)
	w.base, w.complete = w.high, 0
	return expected, complete
}

func videoFeedback(magic string, session uint64, expected, complete int) []byte {
	return fmt.Appendf(nil, "%s STAT %016x %d %d", magic, session, expected, complete)
}

func parseVideoFeedback(packet, magic string, session uint64) (int, bool) {
	fields := strings.Fields(packet)
	if len(fields) != 5 || fields[0] != magic || fields[1] != "STAT" || len(fields[2]) != 16 {
		return 0, false
	}
	id, e := strconv.ParseUint(fields[2], 16, 64)
	expected, a := strconv.Atoi(fields[3])
	complete, b := strconv.Atoi(fields[4])
	if e != nil || id != session || a != nil || b != nil || expected < 6 || expected > 600 || complete < 0 || complete > expected {
		return 0, false
	}
	missing := expected - complete
	if missing == 1 && expected < 12 {
		// A single JPEG can straddle the report boundary at low FPS.
		return -1, true
	}
	return missing * 100 / expected, true
}
