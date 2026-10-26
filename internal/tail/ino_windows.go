//go:build windows

package tail

import "os"

// Windows 上拿不到稳定的 inode（本地开发用），只靠文件头指纹识别。
func fileID(fi os.FileInfo) uint64 { return 0 }
