// Smoke test for TDLib JSON interface via cgo
package main

/*
#cgo CFLAGS: -I/opt/homebrew/Cellar/tdlib/1.8.0/include
#cgo LDFLAGS: -L/opt/homebrew/Cellar/tdlib/1.8.0/lib -ltdjson
#include <td/telegram/td_json_client.h>
#include <stdlib.h>
*/
import "C"
import (
	"encoding/json"
	"fmt"
	"unsafe"
)

func main() {
	fmt.Println("TDLib smoke test starting...")

	// Create client using old API (td_json_client_create)
	client := C.td_json_client_create()
	if client == nil {
		fmt.Println("ERROR: Failed to create TDLib client")
		return
	}
	fmt.Println("TDLib client created successfully")

	// Set log verbosity level (can be called synchronously)
	request := `{"@type": "setLogVerbosityLevel", "new_verbosity_level": 1}`
	cRequest := C.CString(request)
	defer C.free(unsafe.Pointer(cRequest))
	result := C.td_json_client_execute(client, cRequest)
	if result != nil {
		fmt.Printf("setLogVerbosityLevel result: %s\n", C.GoString(result))
	}

	// Get version option (can be called synchronously)
	request = `{"@type": "getOption", "name": "version"}`
	cRequest = C.CString(request)
	defer C.free(unsafe.Pointer(cRequest))
	result = C.td_json_client_execute(client, cRequest)
	if result != nil {
		fmt.Printf("getOption version result: %s\n", C.GoString(result))
		var response map[string]interface{}
		json.Unmarshal([]byte(C.GoString(result)), &response)
		if version, ok := response["value"].(string); ok {
			fmt.Printf("TDLib version: %s\n", version)
		}
	}

	// Get commit hash option
	request = `{"@type": "getOption", "name": "commit_hash"}`
	cRequest = C.CString(request)
	defer C.free(unsafe.Pointer(cRequest))
	result = C.td_json_client_execute(client, cRequest)
	if result != nil {
		fmt.Printf("getOption commit_hash result: %s\n", C.GoString(result))
	}

	// Test td_create_client_id (new API)
	clientID := C.td_create_client_id()
	fmt.Printf("td_create_client_id returned: %d\n", int(clientID))

	// Send a request via new API
	request = `{"@type": "getOption", "name": "version", "@extra": "test123"}`
	cRequest = C.CString(request)
	defer C.free(unsafe.Pointer(cRequest))
	C.td_send(clientID, cRequest)

	// Receive response
	cResult := C.td_receive(2.0)
	if cResult != nil {
		fmt.Printf("td_receive result: %s\n", C.GoString(cResult))
	}

	// Clean up
	C.td_json_client_destroy(client)
	fmt.Println("TDLib client destroyed successfully")
	fmt.Println("Smoke test PASSED!")
}
