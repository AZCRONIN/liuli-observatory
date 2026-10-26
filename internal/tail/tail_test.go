package tail

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func appendLines(t *testing.T, p string, from, to int) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i := from; i < to; i++ {
		fmt.Fprintf(f, "line-%06d\n", i)
	}
}

// drainAll 反复 Poll 并 Commit，直到没有新内容，返回读到的行。
func drainAll(t *testing.T, tl *Tailer) []string {
	t.Helper()
	var out []string
	empty := 0
	for empty < 3 {
		c, err := tl.Poll(64)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range c.Lines {
			out = append(out, string(l))
		}
		tl.Commit(c)
		if len(c.Lines) == 0 {
			empty++
		} else {
			empty = 0
		}
	}
	return out
}

func expectSeq(t *testing.T, got []string, from, to int) {
	t.Helper()
	if len(got) != to-from {
		t.Fatalf("期望 %d 行，实际 %d 行", to-from, len(got))
	}
	for i, l := range got {
		if want := fmt.Sprintf("line-%06d", from+i); l != want {
			t.Fatalf("第 %d 行：期望 %s，实际 %s", i, want, l)
		}
	}
}

func TestResumeAndPartialLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "access.log")
	appendLines(t, p, 0, 100)
	f, _ := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
	f.WriteString("line-000100") // 半行，还没写完
	f.Close()

	tl := New(p, nil)
	expectSeq(t, drainAll(t, tl), 0, 100)
	st := tl.State()
	tl.Close()

	f, _ = os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
	f.WriteString("\n")
	f.Close()
	appendLines(t, p, 101, 150)

	// 模拟重启：用保存的进度重新打开
	tl = New(p, &st)
	defer tl.Close()
	expectSeq(t, drainAll(t, tl), 100, 150)
}

func TestUncommittedIsReread(t *testing.T) {
	p := filepath.Join(t.TempDir(), "access.log")
	appendLines(t, p, 0, 3)
	tl := New(p, nil)
	defer tl.Close()
	c1, _ := tl.Poll(1 << 20)
	// 不 Commit（例如写库失败），再读一次应得到同样的行
	c2, _ := tl.Poll(1 << 20)
	if len(c1.Lines) != 3 || len(c2.Lines) != 3 {
		t.Fatalf("未确认的内容应被重读：%d %d", len(c1.Lines), len(c2.Lines))
	}
}

func TestRenameRotation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不允许重命名已打开的文件，在 Linux 上测试")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "access.log")
	appendLines(t, p, 0, 50)
	tl := New(p, nil)
	defer tl.Close()
	got := drainAll(t, tl)

	// Nginx 在 reopen 之前还会往旧文件写一些
	if err := os.Rename(p, p+".1"); err != nil {
		t.Fatal(err)
	}
	appendLines(t, p+".1", 50, 60)
	appendLines(t, p, 60, 80)
	got = append(got, drainAll(t, tl)...)
	expectSeq(t, got, 0, 80)
	if tl.Rotated != 1 {
		t.Fatalf("期望检测到 1 次切割，实际 %d", tl.Rotated)
	}
}

func TestCopyTruncate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "access.log")
	appendLines(t, p, 0, 40)
	tl := New(p, nil)
	defer tl.Close()
	got := drainAll(t, tl)
	if err := os.Truncate(p, 0); err != nil {
		t.Fatal(err)
	}
	appendLines(t, p, 40, 45)
	got = append(got, drainAll(t, tl)...)
	expectSeq(t, got, 0, 45)
}

func TestTruncateThenGrowBeyondOffset(t *testing.T) {
	p := filepath.Join(t.TempDir(), "access.log")
	appendLines(t, p, 0, 10)
	tl := New(p, nil)
	defer tl.Close()
	got := drainAll(t, tl)
	// 截断后在我们下一次读之前就写得比原来还长
	os.Truncate(p, 0)
	appendLines(t, p, 10, 40)
	got = append(got, drainAll(t, tl)...)
	expectSeq(t, got, 0, 40)
}

func TestRotatedWhileDown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("依赖 inode，在 Linux 上测试")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "access.log")
	appendLines(t, p, 0, 30)
	tl := New(p, nil)
	got := drainAll(t, tl)
	st := tl.State()
	tl.Close()

	// 停机期间：旧文件又写了一些，然后被切割，新文件也写了一些
	appendLines(t, p, 30, 35)
	os.Rename(p, p+".1")
	appendLines(t, p, 35, 50)

	tl = New(p, &st)
	defer tl.Close()
	got = append(got, drainAll(t, tl)...)
	expectSeq(t, got, 0, 50)
}
