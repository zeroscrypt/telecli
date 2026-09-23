//go:build !tdjson_static

package tdclient

/*
#cgo pkg-config: tdjson
#include <td/telegram/td_json_client.h>
#include <stdlib.h>
*/
import "C"

import "unsafe"

// ДИНАМИЧЕСКАЯ линковка через pkg-config (~/.local/tdlib/lib/pkgconfig/tdjson.pc) —
// дефолтный путь локальной разработки. Обёртки над C API TDLib; вся логика клиента —
// в tdclient_common.go. Статический вариант (build tag tdjson_static) — в
// tdclient_static.go, набор обёрток идентичен.

func tdCreateClientID() int32 {
	return int32(C.td_create_client_id())
}

func tdReceive(timeout float64) string {
	cResult := C.td_receive(C.double(timeout))
	if cResult == nil {
		return ""
	}
	return C.GoString(cResult)
}

func tdSend(clientID int32, requestJSON string) {
	cRequest := C.CString(requestJSON)
	defer C.free(unsafe.Pointer(cRequest))
	C.td_send(C.int(clientID), cRequest)
}

func tdExecute(requestJSON string) string {
	cRequest := C.CString(requestJSON)
	defer C.free(unsafe.Pointer(cRequest))
	cResult := C.td_execute(cRequest)
	if cResult == nil {
		return ""
	}
	return C.GoString(cResult)
}
