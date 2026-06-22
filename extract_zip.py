#!/usr/bin/env python3
"""Token Monitor 自更新专用 zip 解压器。

Swift 端自更新流程调起: python3 extract_zip.py <zip_path> <target_dir>
行为:
- 用 Python 标准库 zipfile 解压 (UTF-8 flag 友好)
- 跳过路径含 'windows_build' 的条目 (中文 bat 在 macOS unzip 下解不开)
- 跳过 macOS 资源叉 (_MACOSX 目录, ditto/unzip 都会自动产生但我们用不上)
- 解压过程中记录被跳过的条目到 stderr, 但 exit 0 让 Swift 端认为成功
"""
import os
import sys
import zipfile


def main() -> int:
    if len(sys.argv) != 3:
        print(f"usage: {sys.argv[0]} <zip_path> <target_dir>", file=sys.stderr)
        return 2
    zip_path = sys.argv[1]
    target_dir = sys.argv[2]

    os.makedirs(target_dir, exist_ok=True)

    skipped = 0
    extracted = 0
    try:
        with zipfile.ZipFile(zip_path) as z:
            for info in z.infolist():
                # 跳过目录条目 (zipfile 自己会按需创建)
                if info.is_dir():
                    continue
                # 跳过 windows_build 目录下的所有文件 (中文文件名在 macOS unzip
                # 下解不开, build_macos.sh 也不需要)
                if "/windows_build/" in info.filename or info.filename.endswith("/windows_build"):
                    skipped += 1
                    continue
                # 跳过 macOS 资源叉, 来自 ditto / 系统归档
                if info.filename.startswith("__MACOSX/") or "/__MACOSX/" in info.filename:
                    skipped += 1
                    continue
                # 安全路径: 拒绝 ../ 跳出
                target_path = os.path.join(target_dir, info.filename)
                if not os.path.abspath(target_path).startswith(os.path.abspath(target_dir) + os.sep):
                    print(f"  refusing path traversal: {info.filename}", file=sys.stderr)
                    skipped += 1
                    continue
                # 解压单个文件
                os.makedirs(os.path.dirname(target_path), exist_ok=True)
                with z.open(info) as src, open(target_path, "wb") as dst:
                    dst.write(src.read())
                extracted += 1
    except zipfile.BadZipFile as exc:
        print(f"  不是合法 zip: {exc}", file=sys.stderr)
        return 1
    except Exception as exc:
        print(f"  解压异常: {exc}", file=sys.stderr)
        return 1

    print(f"  extracted={extracted} skipped={skipped}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
