package maintenance

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// IsMountPoint reports whether path itself is a mount point, as listed in
// /proc/self/mountinfo. Bind mounts — including the Docker uploads bind mount —
// are such mount points. On systems without mountinfo (or when it cannot be
// read) it conservatively reports false.
//
// It is used both to select the mount-aware restore protocol and to place the
// maintenance lock inside the persistent mount, so that every container sharing
// the same uploads bind mount contends on the same lock file.
func IsMountPoint(path string) bool {
	target, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		if decodeMountInfoPath(fields[4]) == target {
			return true
		}
	}
	return false
}

// decodeMountInfoPath decodes the octal escapes (\040 space, \011 tab, \012
// newline, \134 backslash) that /proc/self/mountinfo applies to mount paths.
func decodeMountInfoPath(field string) string {
	if !strings.ContainsRune(field, '\\') {
		return field
	}
	var b strings.Builder
	for i := 0; i < len(field); i++ {
		if field[i] == '\\' && i+3 < len(field) {
			if v, err := strconv.ParseUint(field[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(field[i])
	}
	return b.String()
}
