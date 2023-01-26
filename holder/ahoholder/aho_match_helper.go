package ahoholder

import (
	"fmt"
	"strings"
)

func ParseAcMatchDict(values interface{}) (r []string, e error) {
	switch v := values.(type) {
	case string:
		return append(r, v), nil
	case []byte:
		return append(r, string(v)), nil
	case []string:
		return v, nil
	case []interface{}:
		for _, vi := range v {
			if str, ok := vi.(string); ok {
				r = append(r, str)
				continue
			}
			return nil, fmt.Errorf("not string(able) value, value:%+v", vi)
		}
	default:
		return nil, fmt.Errorf("not string(able) value, value:%+v", v)
	}
	return r, nil
}

func BuildAcMatchContent(v interface{}, joinSep string) ([]rune, error) {
	data := make([]rune, 0, 64)
	switch tv := v.(type) {
	case string:
		data = []rune(tv)
	case []string:
		data = []rune(strings.Join(tv, joinSep))
	case []interface{}:
		var ok bool
		var str string
		for idx, vi := range tv {
			if str, ok = vi.(string); !ok {
				return nil, fmt.Errorf("query assign:%+v not string type", v)
			}
			if idx > 0 {
				data = append(data, []rune(joinSep)...)
			}
			data = append(data, []rune(str)...)
		}
	default:
		return nil, fmt.Errorf("query assign:%+v not string type", v)
	}
	return data, nil
}
