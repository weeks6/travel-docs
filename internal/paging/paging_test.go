package paging

import "testing"

func TestPageTotal(t *testing.T) {
	p := Empty[int](Params{Page: 1, PageSize: 20})
	for total, pages := range map[int]int{0: 0, 1: 1, 20: 1, 21: 2} {
		p.SetTotal(total)
		if p.TotalPages != pages {
			t.Errorf("total %d: %d pages, want %d", total, p.TotalPages, pages)
		}
	}
}
