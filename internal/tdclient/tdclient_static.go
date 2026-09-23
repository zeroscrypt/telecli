//go:build tdjson_static

package tdclient

/*
#cgo darwin LDFLAGS: -ltdjson_static -ltdjson_private -ltdclient -ltdcore -ltdactor -ltdapi -ltddb -ltdnet -ltde2e -ltdmtproto -ltdsqlite -ltdutils -lc++ -lssl -lcrypto -lz -ldl -lm
#cgo linux LDFLAGS: -Wl,-Bstatic -ltdjson_static -ltdjson_private -ltdclient -ltdcore -ltdactor -ltdapi -ltddb -ltdnet -ltde2e -ltdmtproto -ltdsqlite -ltdutils -Wl,-Bdynamic -lstdc++ -lssl -lcrypto -ldl -lz -lm
#include <td/telegram/td_json_client.h>
#include <stdlib.h>
*/
import "C"

import "unsafe"

// СТАТИЧЕСКАЯ линковка libtdjson (build tag tdjson_static) — для релизных бинарников, не
// требующих установленной TDLib на машине пользователя. Список либ ниже собран под РЕАЛЬНЫЕ
// статические архивы этой версии TDLib (сверено с ls ~/.local/tdlib/lib/*.a, см. замечание в
// файле задачи 0030): помимо классического набора zelenin/go-tdlib здесь есть отдельные
// libtde2e/libtdmtproto, которых в той версии не было.
//
// Пути -I/-L задаются СНАРУЖИ через CGO_CFLAGS/CGO_LDFLAGS (см. команду сборки в REPORT.md
// задачи 0030): cgo не раскрывает $HOME в строках #cgo, поэтому путь к ~/.local/tdlib
// передаётся переменными окружения при сборке.
//
// Различия платформ: GNU-стиль -Wl,-Bstatic/-Wl,-Bdynamic работает только на Linux; на macOS
// (ld64) таких флагов нет — статические .a подключаются напрямую, а системные библиотеки
// (libc++ и т.п.) остаются динамическими. Обёртки над C API TDLib идентичны tdclient.go —
// вся логика клиента живёт в tdclient_common.go.

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
