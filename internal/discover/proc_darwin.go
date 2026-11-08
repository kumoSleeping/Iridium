package discover

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// defaultProcs is the system's processes: sysctl and proc_info.
func defaultProcs() Procs { return SysProcs{} }

// SysProcs is macOS's processes: sysctl kern.proc for the list and each one's parent, owner and
// start, kern.procargs2 for its command line and environment, proc_info for its folder and open
// files (what libproc's proc_pidinfo does, without cgo).
type SysProcs struct{}

func (SysProcs) PIDs() ([]int, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(procs))
	for _, p := range procs {
		if p.Proc.P_pid > 0 {
			out = append(out, int(p.Proc.P_pid))
		}
	}
	return out, nil
}

// lstart is how `LC_ALL=C TZ=UTC ps -o lstart=` writes a start, which Claude records as procStart.
const lstart = "Mon Jan _2 15:04:05 2006"

func (SysProcs) Stat(pid int) (Stat, error) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return Stat{}, err
	}
	if int(k.Proc.P_pid) != pid {
		return Stat{}, errors.New("no such process")
	}
	tv := k.Proc.P_starttime
	started := time.Unix(int64(tv.Sec), int64(tv.Usec)*1000)
	return Stat{
		PPid:      int(k.Eproc.Ppid),
		Start:     uint64(tv.Sec)*1_000_000 + uint64(tv.Usec),
		Started:   started,
		UID:       int(k.Eproc.Ucred.Uid),
		ProcStart: started.UTC().Format(lstart),
	}, nil
}

// procArgs is kern.procargs2: argc, the executable's path, then argv and the environment, each
// NUL-ended (the path padded with more NULs).
func procArgs(pid int) (exe string, argv, env []string) {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(b) < 4 {
		return "", nil, nil
	}
	argc := int(binary.LittleEndian.Uint32(b))
	b = b[4:]
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return "", nil, nil
	}
	exe, b = string(b[:i]), b[i:]
	for len(b) > 0 && b[0] == 0 {
		b = b[1:]
	}
	for len(b) > 0 {
		i := bytes.IndexByte(b, 0)
		if i < 0 {
			i = len(b)
		}
		s := string(b[:i])
		if i < len(b) {
			b = b[i+1:]
		} else {
			b = nil
		}
		if len(argv) < argc {
			argv = append(argv, s)
			continue
		}
		if s == "" {
			break
		}
		env = append(env, s)
	}
	return exe, argv, env
}

func (SysProcs) Cmdline(pid int) []string {
	_, argv, _ := procArgs(pid)
	return argv
}

func (SysProcs) Exe(pid int) string {
	exe, _, _ := procArgs(pid)
	return exe
}

func (SysProcs) Environ(pid int) []string {
	_, _, env := procArgs(pid)
	return env
}

// proc_info(2), as <sys/proc_info.h> has it.
const (
	sysProcInfo             = 336
	procInfoCallPIDInfo     = 2
	procInfoCallPIDFDInfo   = 3
	procPIDListFDs          = 1
	procPIDVnodePathInfo    = 9
	procPIDFDVnodePathInfo  = 2
	proxFDTypeVnode         = 1
	sizeofVnodeInfo         = 152  // struct vnode_info
	sizeofProcFileInfo      = 24   // struct proc_fileinfo
	maxPathLen              = 1024 // MAXPATHLEN
	sizeofVnodeInfoPath     = sizeofVnodeInfo + maxPathLen
	sizeofProcVnodePathInfo = 2 * sizeofVnodeInfoPath
	sizeofVnodeFDInfoPath   = sizeofProcFileInfo + sizeofVnodeInfoPath
	sizeofProcFDInfo        = 8
)

func procInfo(call, pid, flavor int, arg uint64, buf []byte) (int, error) {
	var p uintptr
	if len(buf) > 0 {
		p = uintptr(unsafe.Pointer(&buf[0]))
	}
	n, _, e := syscall.Syscall6(sysProcInfo, uintptr(call), uintptr(pid), uintptr(flavor), uintptr(arg), p, uintptr(len(buf)))
	if e != 0 {
		return 0, e
	}
	return int(n), nil
}

// cString is the NUL-ended string at the start of b.
func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func (SysProcs) Cwd(pid int) string {
	buf := make([]byte, sizeofProcVnodePathInfo)
	n, err := procInfo(procInfoCallPIDInfo, pid, procPIDVnodePathInfo, 0, buf)
	if err != nil || n < sizeofVnodeInfoPath {
		return ""
	}
	return cString(buf[sizeofVnodeInfo:sizeofVnodeInfoPath])
}

func (SysProcs) Files(pid int) []string {
	need, err := procInfo(procInfoCallPIDInfo, pid, procPIDListFDs, 0, nil)
	if err != nil || need <= 0 {
		return nil
	}
	fds := make([]byte, need+32*sizeofProcFDInfo)
	n, err := procInfo(procInfoCallPIDInfo, pid, procPIDListFDs, 0, fds)
	if err != nil {
		return nil
	}
	var out []string
	vb := make([]byte, sizeofVnodeFDInfoPath)
	for i := 0; i+sizeofProcFDInfo <= n; i += sizeofProcFDInfo {
		fd := int32(binary.LittleEndian.Uint32(fds[i:]))
		if binary.LittleEndian.Uint32(fds[i+4:]) != proxFDTypeVnode {
			continue
		}
		clear(vb)
		// proc_pidfdinfo: the flavor comes before the descriptor.
		if m, err := procInfo(procInfoCallPIDFDInfo, pid, procPIDFDVnodePathInfo, uint64(fd), vb); err != nil || m < sizeofVnodeFDInfoPath {
			continue
		}
		if p := cString(vb[sizeofProcFileInfo+sizeofVnodeInfo:]); strings.HasPrefix(p, "/") {
			out = append(out, p)
		}
	}
	return out
}
