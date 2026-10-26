#!/usr/bin/env python3
"""打包上传到服务器用的 liuli-obs.zip（在项目根目录运行：python scripts/pack.py）。

只包含 git 管理的文件（用工作区里的当前内容），不含 node_modules、data、demo、bin 等。
文件名按 UTF-8 写入，1Panel 解压后中文文件名不会乱码。
"""
import os
import subprocess
import zipfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT = os.path.join(ROOT, "liuli-obs.zip")
# 服务器上用不到的
SKIP_PREFIX = ("docs/", ".github/", "CLAUDE.md")


def main():
    files = subprocess.run(["git", "-c", "core.quotepath=false", "ls-files", "-z"], cwd=ROOT,
                           capture_output=True, check=True).stdout.decode("utf-8").split("\0")
    files = [f for f in files if f and not f.startswith(SKIP_PREFIX) and os.path.isfile(os.path.join(ROOT, f))]
    if os.path.exists(OUT):
        os.remove(OUT)
    with zipfile.ZipFile(OUT, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
        for f in sorted(files):
            with open(os.path.join(ROOT, f), "rb") as fh:
                data = fh.read()
            # Windows 上编辑过的文本文件统一成 LF，服务器上的脚本和配置不会带 \r
            if b"\0" not in data:
                data = data.replace(b"\r\n", b"\n")
            info = zipfile.ZipInfo(f, date_time=(2026, 1, 1, 0, 0, 0))
            info.external_attr = 0o644 << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            z.writestr(info, data)
    print(f"已生成 {OUT}：{len(files)} 个文件，{os.path.getsize(OUT) / 1024 / 1024:.1f} MB")


if __name__ == "__main__":
    main()
