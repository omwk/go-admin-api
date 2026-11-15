package system

import (
	"reflect"
	"strings"
)

// System dictionary edits only change display attributes; new fields default to read-only.
var systemDictWritableFields = map[string]struct{}{
	"name": {}, "status": {},
}

var systemDictItemWritableFields = map[string]struct{}{
	"label": {}, "sort": {}, "color": {}, "status": {}, "extra": {},
}

func validateDictFields(before, after any, writable map[string]struct{}) error {
	old, next := reflect.ValueOf(before), reflect.ValueOf(after)
	if old.Type() != next.Type() || old.Kind() != reflect.Struct {
		return invalidDict()
	}
	for i := 0; i < old.NumField(); i++ {
		field := old.Type().Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if _, allowed := writable[name]; allowed {
			continue
		}
		if !reflect.DeepEqual(old.Field(i).Interface(), next.Field(i).Interface()) {
			return dictConflict("系统字典仅允许修改展示字段")
		}
	}
	return nil
}
