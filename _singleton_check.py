#!/usr/bin/env python3
"""Token Monitor 单实例锁原子检查器.

start.sh 在用户层快速判断 "server.py 是不是已经在跑".
成功取到锁 → 退出 0 (start.sh 可以继续拉起 server.py).
拿不到锁   → 退出 1 (start.sh 应该给友好提示并退出).
打开文件失败 → 退出 2.

故意把锁 fd 留到进程退出, 锁的释放由内核回收, 不污染其他检查.
真正的 "持锁到 server.py 退出" 是 server.py 自己的事, 本脚本
只负责 start.sh 那一瞬间的判断.
"""
import fcntl
import os
import sys


def main() -> int:
    if len(sys.argv) < 2:
        print(f"usage: {sys.argv[0]} <lock_file>", file=sys.stderr)
        return 2
    lock_path = sys.argv[1]
    try:
        fd = open(lock_path, "w")
    except OSError as exc:
        print(f"[singleton_check] cannot open {lock_path}: {exc}", file=sys.stderr)
        return 2
    try:
        fcntl.flock(fd.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
    except (IOError, OSError):
        fd.close()
        return 1
    fd.write(f"{os.getpid()}\n")
    fd.flush()
    # 持有 fd 引用直到进程退出, 由内核释放锁
    return 0


if __name__ == "__main__":
    sys.exit(main())
