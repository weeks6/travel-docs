// Package paging describes one page of a list, shared by the stores that
// list things and the handlers that parse ?page and ?page_size.
package paging

// Limits shared by every list endpoint.
const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// Params is the requested page of a list.
type Params struct {
	Page     int // 1-based
	PageSize int
}

// Offset is the number of items before the page.
func (p Params) Offset() int { return (p.Page - 1) * p.PageSize }

// Page is one page of a list as returned by the API.
type Page[T any] struct {
	Items      []T `json:"items"`
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

// Empty returns a page with no items (encoded as [] rather than null).
func Empty[T any](p Params) Page[T] {
	return Page[T]{Items: []T{}, Page: p.Page, PageSize: p.PageSize}
}

// SetTotal records the total item count and derives TotalPages.
func (p *Page[T]) SetTotal(total int) {
	p.Total = total
	p.TotalPages = (total + p.PageSize - 1) / p.PageSize
}
