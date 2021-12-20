package utils

func Must(v interface{}, err error) interface{} {
	if err != nil {
		panic(err)
	}
	return v
}
