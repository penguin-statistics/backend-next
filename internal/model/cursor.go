package model

type Cursor struct {
	Start int // pointer to the first item for the previous page
	End   int // pointer to the last item for the next page
}
