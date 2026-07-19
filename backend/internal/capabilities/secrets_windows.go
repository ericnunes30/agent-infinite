//go:build windows

package capabilities

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func protectSecret(plain []byte) ([]byte, error) {
	input := windows.DataBlob{Size: uint32(len(plain))}
	if len(plain) > 0 {
		input.Data = &plain[0]
	}
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}

func unprotectSecret(protected []byte) ([]byte, error) {
	input := windows.DataBlob{Size: uint32(len(protected))}
	if len(protected) > 0 {
		input.Data = &protected[0]
	}
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	return append([]byte(nil), unsafe.Slice(output.Data, output.Size)...), nil
}
