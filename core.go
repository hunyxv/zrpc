package zrpc

import "reflect"

type MethodFunc struct {
	Method reflect.Value   // function
	Params []reflect.Value // params
}

func (f MethodFunc) Call() (ret []interface{}) {
	for _, item := range f.Method.Call(f.Params) {
		ret = append(ret, item.Interface())
	}
	return
}
