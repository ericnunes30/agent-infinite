package terminal

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func HasDescendants(rootPID int) bool {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	parents := make(map[uint32]uint32)
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return false
	}
	for {
		parents[entry.ProcessID] = entry.ParentProcessID
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	root := uint32(rootPID)
	for process := range parents {
		for parent := parents[process]; parent != 0; parent = parents[parent] {
			if parent == root {
				return true
			}
			if parent == process {
				break
			}
			process = parent
		}
	}
	return false
}
