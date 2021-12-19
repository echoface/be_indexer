package util

import "encoding/json"

func JSONString(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func JSONPretty(v interface{}) string {
	data, _ := json.MarshalIndent(v, "", " ")
	return string(data)
}
