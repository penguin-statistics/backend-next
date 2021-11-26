package helpers

import (
	"encoding/json"
)

type NullIntSlice struct {
	Slice []int
	Valid bool
}

func (nis *NullIntSlice) MarshalJSON() ([]byte, error) {
	if !nis.Valid {
		return json.Marshal(nil)
	}
	return json.Marshal(nis.Slice)
}

func NewIntSlice(slice []int, valid bool) *NullIntSlice {
	return &NullIntSlice {
		Slice: slice,
		Valid: valid,
	}
}
