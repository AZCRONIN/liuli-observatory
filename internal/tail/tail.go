// Package tail 跟读一个日志文件：断点续读（inode + 偏移量 + 文件头指纹），
// 能跟上 logrotate 的两种切割方式（改名后新建 / copytruncate），
// 并且只有在调用方确认写入成功（Commit）后才前进偏移量，保证不丢不重。
package tail

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State 是需要持久化的读取进度。
type State struct {
	Path   string
	Ino    uint64 // 文件 inode；Windows 上为 0，只靠指纹判断
	Offset int64
	FP     string // 文件开头 FPLen 字节的指纹，用来识别 inode 被复用或文件被替换
	FPLen  int
}

const fpMax = 256

// 切割后旧文件还会被 Nginx 写一小会儿（重新打开日志之前），继续读它这么久。
const drainGrace = 30 * time.Second

// Chunk 是一次读取的结果。处理完 Lines 后必须调用 Commit，进度才会前进。
type Chunk struct {
	Lines [][]byte
	State State // 读完这批后应持久化的进度

	fromOld bool
	oldOff  int64
}

type drain struct {
	f        *os.File
	off      int64
	lastGrow time.Time
}

// Tailer 跟读单个路径。非并发安全，由一个 goroutine 驱动。
type Tailer struct {
	path    string
	st      State
	f       *os.File
	old     *drain
	pending *State
	// Rotated 记录发生过的切割次数，方便测试和排障。
	Rotated int
}

// New 创建 Tailer。saved 为上次保存的进度，没有则传 nil（从文件开头读）。
func New(path string, saved *State) *Tailer {
	t := &Tailer{path: path, st: State{Path: path}}
	if saved != nil {
		t.st = *saved
		t.st.Path = path
	}
	return t
}

// Path 返回跟读的路径。
func (t *Tailer) Path() string { return t.path }

// Close 关闭打开的文件。
func (t *Tailer) Close() {
	if t.f != nil {
		t.f.Close()
		t.f = nil
	}
	if t.old != nil {
		t.old.f.Close()
		t.old = nil
	}
}

// Poll 最多读取 max 字节的完整行。文件还不存在时返回空结果、无错误。
func (t *Tailer) Poll(max int) (Chunk, error) {
	if t.f == nil {
		if err := t.open(); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return Chunk{State: t.st}, nil
			}
			return Chunk{State: t.st}, err
		}
	}
	// 先把切割下来的旧文件读完
	if t.old != nil {
		lines, n, err := readLines(t.old.f, t.old.off, max)
		if err != nil {
			t.old.f.Close()
			t.old = nil
		} else if n > 0 {
			t.old.lastGrow = time.Now()
			return Chunk{Lines: lines, State: t.st, fromOld: true, oldOff: t.old.off + n}, nil
		} else if time.Since(t.old.lastGrow) > drainGrace {
			t.old.f.Close()
			t.old = nil
		}
	}

	// 文件头变了：被截断后又写入了新内容，或被原地替换，从头读
	if t.st.FPLen > 0 {
		if fp, n := fingerprint(t.f, t.st.FPLen); n < t.st.FPLen || fp != t.st.FP {
			t.st.Offset, t.st.FP, t.st.FPLen = 0, "", 0
			t.Rotated++
		}
	}

	lines, n, err := readLines(t.f, t.st.Offset, max)
	if err != nil {
		return Chunk{State: t.st}, err
	}
	if n > 0 {
		next := t.st
		next.Offset += n
		if next.FPLen < fpMax {
			next.FP, next.FPLen = fingerprint(t.f, fpMax)
		}
		return Chunk{Lines: lines, State: next}, nil
	}

	// 没有新内容：检查是否被切割或截断
	fi, err := t.f.Stat()
	if err != nil {
		return Chunk{State: t.st}, err
	}
	if pfi, err := os.Stat(t.path); err == nil && !os.SameFile(pfi, fi) {
		// 改名切割：旧文件已读到末尾，转为「排空」状态，再打开新文件
		t.old = &drain{f: t.f, off: t.st.Offset, lastGrow: time.Now()}
		t.f = nil
		t.st = State{Path: t.path}
		t.Rotated++
		if err := t.open(); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Chunk{State: t.st}, err
		}
		return Chunk{State: t.st}, nil
	}
	if fi.Size() < t.st.Offset {
		// copytruncate：文件被截断，从头读
		t.st.Offset, t.st.FP, t.st.FPLen = 0, "", 0
		t.Rotated++
		return Chunk{State: t.st}, nil
	}
	return Chunk{State: t.st}, nil
}

// Commit 确认 Chunk 已经处理完毕，前进进度。
func (t *Tailer) Commit(c Chunk) {
	if c.fromOld {
		if t.old != nil {
			t.old.off = c.oldOff
		}
		return
	}
	if c.State.Path == t.st.Path && c.State.Ino == t.st.Ino {
		t.st = c.State
	}
}

// State 返回当前进度。
func (t *Tailer) State() State { return t.st }

func (t *Tailer) open() error {
	f, err := os.Open(t.path)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	ino := fileID(fi)
	saved := t.st
	fresh := saved.Offset == 0 && saved.FPLen == 0
	t.f = f
	if fresh {
		t.st = State{Path: t.path, Ino: ino}
		return nil
	}
	if sameFile(f, ino, saved) {
		t.st.Ino = ino
		if fi.Size() < saved.Offset {
			t.st.Offset, t.st.FP, t.st.FPLen = 0, "", 0
		}
		return nil
	}
	// 停机期间发生了切割：在同目录找到旧文件，把剩下的部分读完
	if old := findRotated(t.path, saved); old != nil {
		t.old = &drain{f: old, off: saved.Offset, lastGrow: time.Now()}
	}
	t.st = State{Path: t.path, Ino: ino}
	t.Rotated++
	return nil
}

func sameFile(f *os.File, ino uint64, s State) bool {
	if ino != 0 && s.Ino != 0 && ino != s.Ino {
		return false
	}
	if s.FPLen > 0 {
		fp, n := fingerprint(f, s.FPLen)
		if n < s.FPLen || fp != s.FP {
			return false
		}
	}
	return true
}

// findRotated 在日志所在目录里找 inode（或指纹）和保存的进度一致的文件，例如 access.log.1。
func findRotated(path string, s State) *os.File {
	dir, base := filepath.Dir(path), filepath.Base(path)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range ents {
		n := e.Name()
		if n == base || !strings.HasPrefix(n, base) || strings.HasSuffix(n, ".gz") || e.IsDir() {
			continue
		}
		f, err := os.Open(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		fi, err := f.Stat()
		if err == nil && fi.Size() >= s.Offset {
			ino := fileID(fi)
			if (s.Ino == 0 || ino == s.Ino) && sameFile(f, ino, s) {
				return f
			}
		}
		f.Close()
	}
	return nil
}

func fingerprint(f *os.File, n int) (string, int) {
	buf := make([]byte, n)
	m, _ := f.ReadAt(buf, 0)
	if m == 0 {
		return "", 0
	}
	h := sha1.Sum(buf[:m])
	return hex.EncodeToString(h[:8]), m
}

// readLines 从 off 开始读最多 max 字节，只返回完整的行，n 为消费的字节数。
func readLines(f *os.File, off int64, max int) ([][]byte, int64, error) {
	buf := make([]byte, max)
	m, err := f.ReadAt(buf, off)
	if err != nil && err != io.EOF {
		return nil, 0, err
	}
	buf = buf[:m]
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		if m == max {
			// 单行超过缓冲区：整段当作一行丢给解析器（会解析失败并被计入错误）
			return [][]byte{buf}, int64(m), nil
		}
		return nil, 0, nil
	}
	data := buf[:end]
	var lines [][]byte
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		var l []byte
		if i < 0 {
			l, data = data, nil
		} else {
			l, data = data[:i], data[i+1:]
		}
		l = bytes.TrimRight(l, "\r")
		if len(l) > 0 {
			lines = append(lines, l)
		}
	}
	return lines, int64(end + 1), nil
}
