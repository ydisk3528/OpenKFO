#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""把一段 XML 插到 config.spf2 里某个条目的指定锚点之后。

专为「往 ustate.xml 末尾的 431 号 <Data> 之后追加 432」这类场景写。
锚点用正则找，插入点 = 锚点匹配结束的位置。

用法：
  Insert-Spf2-Xml.py <spf2> --entry ustate.xml \
      --after '<Data type="431".*?</Data>' --insert-file 片段.xml --out 新文件
"""

import argparse
import os
import re
import shutil
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from importlib import util as _util

# 复用 Patch-Spf2-Lua.py 的归档读写实现
_spec = _util.spec_from_file_location(
    "patchspf2", os.path.join(os.path.dirname(os.path.abspath(__file__)), "Patch-Spf2-Lua.py")
)
_p = _util.module_from_spec(_spec)
_spec.loader.exec_module(_p)


def main():
    ap = argparse.ArgumentParser(description="往 SGDP 条目里插 XML 片段")
    ap.add_argument("path")
    ap.add_argument("--entry", required=True, help="目标条目（客户端路径写法）")
    ap.add_argument("--after", required=True, help="锚点正则；插入点在其匹配结束处")
    ap.add_argument("--insert-file", required=True, help="要插入的片段（UTF-8）")
    ap.add_argument("--out")
    ap.add_argument("--in-place", action="store_true")
    ap.add_argument("--compact", action="store_true", help="写出前先紧凑化（回收孤儿块）")
    ap.add_argument("--dry-run", action="store_true", help="只打印将要插入的位置，不写")
    args = ap.parse_args()

    data = open(args.path, "rb").read()
    tree, table, cks, count = _p.read_header(data)
    entries = _p.read_tree(data, tree, table)

    key = args.entry.replace("\\", "/").lstrip("/").lower()
    if key not in entries:
        raise SystemExit(f"没有这个条目：{key}")
    index = entries[key]
    text = _p.entry_text(data, table, index)

    matches = list(re.finditer(args.after, text, re.S))
    if not matches:
        raise SystemExit(f"锚点没匹配到：{args.after}")
    if len(matches) > 1:
        print(f"⚠ 锚点匹配到 {len(matches)} 处，使用最后一处")
    anchor = matches[-1]
    at = anchor.end()

    addition = open(args.insert_file, encoding="utf-8").read()
    addition = addition.replace("\r\n", "\n").replace("\n", "\r\n")
    if not addition.endswith("\r\n"):
        addition += "\r\n"

    print(f"条目 {key}（{len(text)} 字符），锚点匹配：{anchor.group(0)[:60]!r}")
    print(f"插入点偏移 {at}，前后文：")
    print(f"  ← {text[max(0, at - 40):at]!r}")
    print(f"  → {text[at:at + 40]!r}")
    print(f"待插入 {len(addition)} 字符：")
    print(addition)

    if args.dry_run:
        return 0

    merged = text[:at] + addition + text[at:]
    # 防重复
    if f'type="432"' in text:
        raise SystemExit('条目里已经有 type="432"，中止（避免重复插入）')

    patched = _p.replace_entries(data, {index: merged})
    if args.compact:
        patched = _p.compact(patched)
    r = _p.verify(patched)
    if not r["ok"]:
        raise SystemExit(f"校验失败：CRC 失败 {r['bad_crc']} 条")

    if args.in_place:
        backup = f"{args.path}.bak-{time.strftime('%Y%m%d-%H%M%S')}"
        shutil.copy2(args.path, backup)
        print(f"已备份 → {backup}")
        dst = args.path
    elif args.out:
        dst = args.out
    else:
        raise SystemExit("请指定 --out 或 --in-place")

    open(dst, "wb").write(patched)
    print(f"✅ 已写入 {dst}（{len(data)} → {len(patched)} 字节）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
