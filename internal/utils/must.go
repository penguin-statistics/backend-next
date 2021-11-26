package utils

import "log"

func Must(v interface{}, err error) interface{} {
	if err != nil {
		log.Fatalln(err)
	}
	return v
}
