package rules

import "sort"

// rank orders the severities, most severe highest; unknown ones rank 0.
func (s Severity) rank() int {
	switch s {
	case SeverityError:
		return 3
	case SeverityWarn:
		return 2
	case SeverityInfo:
		return 1
	}
	return 0
}

// AtLeast reports whether s is a known severity at least as severe as floor.
func (s Severity) AtLeast(floor Severity) bool {
	return s.rank() > 0 && s.rank() >= floor.rank()
}

// NewSince returns the findings of cur that are not in base (by Key) and are
// at least floor, most severe first, then by key: the debt added since base.
func NewSince(base, cur []Finding, floor Severity) []Finding {
	added, _ := Diff(base, cur)
	out := make([]Finding, 0, len(added))
	for _, f := range added {
		if Severity(f.Severity).AtLeast(floor) {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := Severity(out[i].Severity).rank(), Severity(out[j].Severity).rank()
		if ri != rj {
			return ri > rj
		}
		return out[i].Key() < out[j].Key()
	})
	return out
}
