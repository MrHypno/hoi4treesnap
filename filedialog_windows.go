//go:build windows

package main

import (
	"errors"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// Native file and folder pickers.
//
// The dialogs used to come from a helper library that called the old
// SHBrowseForFolder tree for folders, opened its windows without an owner and
// ran them on the UI goroutine. That had three consequences: the folder picker
// looked nothing like the file picker, the dialog could end up behind the main
// window, and while it was open the window ignored its own close button
// because Fyne's event loop was stuck inside the dialog.
//
// These call IFileOpenDialog directly, on their own thread and with the main
// window as the owner, which fixes all three.

var (
	modOle32    = syscall.NewLazyDLL("ole32.dll")
	modShell32  = syscall.NewLazyDLL("shell32.dll")
	modUser32   = syscall.NewLazyDLL("user32.dll")
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCoInitializeEx           = modOle32.NewProc("CoInitializeEx")
	procCoUninitialize           = modOle32.NewProc("CoUninitialize")
	procCoCreateInstance         = modOle32.NewProc("CoCreateInstance")
	procCoTaskMemFree            = modOle32.NewProc("CoTaskMemFree")
	procSHCreateItemFromParsing  = modShell32.NewProc("SHCreateItemFromParsingName")
	procEnumWindows              = modUser32.NewProc("EnumWindows")
	procGetWindowThreadProcessID = modUser32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible          = modUser32.NewProc("IsWindowVisible")
	procGetCurrentProcessID      = modKernel32.NewProc("GetCurrentProcessId")
)

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	clsidFileOpenDialog = guid{0xDC1C5A9C, 0xE88A, 0x4DDE, [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidFileOpenDialog   = guid{0xD57C7288, 0xD4AD, 0x4768, [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	iidShellItem        = guid{0x43826D1E, 0xE718, 0x42EE, [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
)

const (
	coinitApartmentThreaded = 0x2
	coinitDisableOLE1DDE    = 0x4
	clsctxInprocServer      = 0x1

	fosPickFolders     = 0x00000020
	fosForceFileSystem = 0x00000040
	fosAllowMultiSelect = 0x00000200
	fosPathMustExist   = 0x00000800
	fosFileMustExist   = 0x00001000

	sigdnFileSysPath = 0x80058000

	sOK              = 0
	sFalse           = 1
	errorCancelled   = 0x800704C7
	rpcEChangedMode  = 0x80010106
)

// vtable slots. IFileOpenDialog inherits IFileDialog, which inherits
// IModalWindow, which inherits IUnknown.
const (
	slotRelease      = 2
	slotShow         = 3
	slotSetFileTypes = 4
	slotSetOptions   = 9
	slotGetOptions   = 10
	slotSetFolder    = 12
	slotSetTitle     = 17
	slotGetResult    = 20
	slotGetResults   = 27

	slotItemGetDisplayName = 5

	slotArrayGetCount  = 7
	slotArrayGetItemAt = 8
)

// vtblCall invokes a COM method by its slot in the object's vtable. COM
// objects are held as unsafe.Pointer rather than uintptr so no address ever
// lives in an integer across a call.
func vtblCall(obj unsafe.Pointer, slot int, args ...uintptr) uintptr {
	vtbl := *(**[64]uintptr)(obj)
	call := append([]uintptr{uintptr(obj)}, args...)
	r, _, _ := syscall.SyscallN(vtbl[slot], call...)
	return r
}

type comdlgFilterSpec struct {
	Name *uint16
	Spec *uint16
}

// mainWindowHandle finds the top level window of this process so the dialog
// can be owned by it. Fyne draws through GLFW, whose windows use that class.
func mainWindowHandle() uintptr {
	selfPID, _, _ := procGetCurrentProcessID.Call()
	var found uintptr

	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var pid uint32
		procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if uintptr(pid) != selfPID {
			return 1
		}
		if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
			return 1
		}
		found = hwnd
		return 0
	})
	procEnumWindows.Call(cb, 0)
	return found
}

// runDialog performs the COM work on a thread of its own so the UI keeps
// running while the dialog is open.
func runDialog(fn func(owner uintptr) ([]string, error)) ([]string, error) {
	owner := mainWindowHandle()

	type result struct {
		paths []string
		err   error
	}
	done := make(chan result, 1)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
		switch uint32(hr) {
		case sOK, sFalse:
			defer procCoUninitialize.Call()
		case rpcEChangedMode:
			// Another apartment model is already set on this thread; the
			// dialog still works, we just must not uninitialise it.
		default:
			done <- result{nil, errors.New("COM could not be initialised")}
			return
		}

		paths, err := fn(owner)
		done <- result{paths, err}
	}()

	r := <-done
	return r.paths, r.err
}

func openDialog(owner uintptr, title string, options uint32, startDir string, filters []comdlgFilterSpec) ([]string, error) {
	var dialog unsafe.Pointer
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidFileOpenDialog)),
		uintptr(unsafe.Pointer(&dialog)),
	)
	if uint32(hr) != sOK || dialog == nil {
		return nil, errors.New("the file dialog could not be created")
	}
	defer vtblCall(dialog, slotRelease)

	var current uint32
	vtblCall(dialog, slotGetOptions, uintptr(unsafe.Pointer(&current)))
	vtblCall(dialog, slotSetOptions, uintptr(current|options))

	if title != "" {
		if p, err := syscall.UTF16PtrFromString(title); err == nil {
			vtblCall(dialog, slotSetTitle, uintptr(unsafe.Pointer(p)))
		}
	}

	if len(filters) > 0 {
		vtblCall(dialog, slotSetFileTypes, uintptr(len(filters)), uintptr(unsafe.Pointer(&filters[0])))
	}

	if startDir != "" && dirExists(startDir) {
		if item, err := shellItemFromPath(startDir); err == nil {
			vtblCall(dialog, slotSetFolder, uintptr(item))
			vtblCall(item, slotRelease)
		}
	}

	hr = vtblCall(dialog, slotShow, owner)
	if uint32(hr) == errorCancelled {
		return nil, errDialogCancelled
	}
	if uint32(hr) != sOK {
		return nil, errors.New("the file dialog failed")
	}

	if options&fosAllowMultiSelect == 0 {
		var item unsafe.Pointer
		if hr := vtblCall(dialog, slotGetResult, uintptr(unsafe.Pointer(&item))); uint32(hr) != sOK || item == nil {
			return nil, errors.New("the selection could not be read")
		}
		defer vtblCall(item, slotRelease)
		p, err := displayName(item)
		if err != nil {
			return nil, err
		}
		return []string{p}, nil
	}

	var array unsafe.Pointer
	if hr := vtblCall(dialog, slotGetResults, uintptr(unsafe.Pointer(&array))); uint32(hr) != sOK || array == nil {
		return nil, errors.New("the selection could not be read")
	}
	defer vtblCall(array, slotRelease)

	var count uint32
	vtblCall(array, slotArrayGetCount, uintptr(unsafe.Pointer(&count)))

	out := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		var item unsafe.Pointer
		if hr := vtblCall(array, slotArrayGetItemAt, uintptr(i), uintptr(unsafe.Pointer(&item))); uint32(hr) != sOK || item == nil {
			continue
		}
		p, err := displayName(item)
		vtblCall(item, slotRelease)
		if err == nil && p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, errDialogCancelled
	}
	return out, nil
}

func shellItemFromPath(path string) (unsafe.Pointer, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var item unsafe.Pointer
	hr, _, _ := procSHCreateItemFromParsing.Call(
		uintptr(unsafe.Pointer(p)),
		0,
		uintptr(unsafe.Pointer(&iidShellItem)),
		uintptr(unsafe.Pointer(&item)),
	)
	if uint32(hr) != sOK || item == nil {
		return nil, errors.New("path could not be resolved")
	}
	return item, nil
}

func displayName(item unsafe.Pointer) (string, error) {
	var wide *uint16
	if hr := vtblCall(item, slotItemGetDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&wide))); uint32(hr) != sOK || wide == nil {
		return "", errors.New("path could not be read")
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(wide)))
	return utf16PtrToString(wide), nil
}

// utf16PtrToString reads a null terminated wide string the shell allocated.
func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	n := 0
	for q := unsafe.Pointer(p); *(*uint16)(q) != 0; q = unsafe.Add(q, 2) {
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice(p, n))
}

// pickFilesNative shows the file picker.
func pickFilesNative(title, filterName string, extensions []string, startDir string, multi bool) ([]string, error) {
	var filters []comdlgFilterSpec
	if len(extensions) > 0 {
		var patterns []string
		for _, e := range extensions {
			patterns = append(patterns, "*."+strings.TrimPrefix(e, "."))
		}
		name, _ := syscall.UTF16PtrFromString(filterName + " (" + strings.Join(patterns, ";") + ")")
		spec, _ := syscall.UTF16PtrFromString(strings.Join(patterns, ";"))
		allName, _ := syscall.UTF16PtrFromString("All files (*.*)")
		allSpec, _ := syscall.UTF16PtrFromString("*.*")
		filters = []comdlgFilterSpec{{name, spec}, {allName, allSpec}}
	}

	options := uint32(fosForceFileSystem | fosFileMustExist | fosPathMustExist)
	if multi {
		options |= fosAllowMultiSelect
	}

	return runDialog(func(owner uintptr) ([]string, error) {
		return openDialog(owner, title, options, startDir, filters)
	})
}

// pickFolderNative shows the same explorer window in folder mode.
func pickFolderNative(title, startDir string) (string, error) {
	paths, err := runDialog(func(owner uintptr) ([]string, error) {
		return openDialog(owner, title, fosPickFolders|fosForceFileSystem|fosPathMustExist, startDir, nil)
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", errDialogCancelled
	}
	return paths[0], nil
}

// askForFiles and askForFolder are what the window calls. They return at once
// and hand the answer back on the Fyne goroutine, so the window stays alive
// and closable while the dialog is up.

func askForFiles(u *ui, title string, exts []string, startDir string, multi bool, done func([]string)) {
	go func() {
		paths, err := pickFilesNative(title, "Focus tree file", exts, startDir, multi)
		u.dialogResult(err, func() { done(paths) })
	}()
}

func askForFolder(u *ui, title, startDir string, done func(string)) {
	go func() {
		path, err := pickFolderNative(title, startDir)
		u.dialogResult(err, func() { done(path) })
	}()
}
