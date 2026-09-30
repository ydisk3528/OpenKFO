#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Patch-Spf2-Lua.py —— 往 config.spf2 的**已有** lua 条目末尾追加函数。

SGDP 布局（见 OpenKFO/server/go-server/internal/desktop/archive.go）：
  [0,40)  头：magic "SGDP Package File.\\0" + 版本 2
  [40,56) tree / table / checksums / count
  [56,60) 汇总 CRC = crc32(校验表每条 4 字节 CRC 拼起来)
  [60,64) 尾标记 8f ec 07 1f
  [tree,table)      目录树（8 字节 label + 4 字节条目号，0xFF000000 表示叶子）
  [table,checksums) 索引：每 8 字节 (offset, size)
  [checksums,len)   校验表：每 8 字节 (crc32, 保留)

条目块格式：4 字节 0x00002200 + zlib(GB18030 字节流，逐字节异或 255^configKey[i%32])
ASCII 之外的字符用 GB18030 编码。

写入策略：**与 archive.replace() 一致**——把新块追加到数据区末尾，
只改索引表指向；同时提供 --compact 回收旧块。

用法：
  Patch-Spf2-Lua.py --list <spf2>                       # 列出 lua 条目
  Patch-Spf2-Lua.py --show <spf2> <条目名>               # 打印条目文本
  Patch-Spf2-Lua.py <spf2> --append-file <片段.lua> \
        --entry script/playereventproc/ustateeventproc.lua \
        --out <新文件>                                   # 追加并另存
"""

import argparse
import os
import re
import shutil
import struct
import sys
import time
import zlib

MAGIC = b"SGDP Package File.\x00"
CONFIG_KEY = b"F48A715746514613BD70EF276119128F"
TAIL = bytes.fromhex("8fec071f")
BLOCK_HEAD = b"\x00\x22\x00\x00"

# 32 张 256 字节翻译表：块内下标 % 32 == p 的字节用第 p 张（走 bytes.translate）
XOR_TABLES = tuple(
    bytes(c ^ (255 ^ CONFIG_KEY[p % len(CONFIG_KEY)]) for c in range(256))
    for p in range(32)
)


def veil(body):
    """异或混淆（揭/盖同一函数）。body 必须从块内下标 0 开始。"""
    out = bytearray(len(body))
    for p in range(32):
        out[p::32] = body[p::32].translate(XOR_TABLES[p])
    return bytes(out)


def read_header(data):
    if len(data) < 64 or data[: len(MAGIC)] != MAGIC:
        raise SystemExit("不是 SGDP 包（magic 不匹配）")
    tree, table, cks, count = struct.unpack_from("<IIII", data, 40)
    if not (64 <= tree <= table < cks):
        raise SystemExit(f"区间不合法：tree={tree} table={table} checksums={cks}")
    if table + count * 8 != cks or cks + count * 8 != len(data):
        raise SystemExit(
            f"边界不自洽：table+count*8={table + count * 8} cks={cks} "
            f"cks+count*8={cks + count * 8} len={len(data)}"
        )
    return tree, table, cks, count


def read_tree(data, tree, table):
    """返回 {路径小写: 条目号}，与 archive.go 的 parseArchive 同语义。"""
    entries = {}
    pending = [(0, "")]
    seen = set()
    while pending:
        offset, prefix = pending.pop()
        if offset in seen:
            raise SystemExit("目录树指针重复")
        seen.add(offset)
        if offset + 4 > table:
            raise SystemExit("目录树越界")
        size = struct.unpack_from("<I", data, tree + offset)[0]
        end = offset + 4 + size * 12
        if end > table - tree:
            raise SystemExit("目录树节点越界")
        for pos in range(offset + 4, end, 12):
            label = data[tree + pos : tree + pos + 8]
            zero = label.find(b"\x00")
            if zero >= 0:
                label = label[:zero]
            path = prefix + label.decode("gb18030", "replace")
            target = struct.unpack_from("<I", data, tree + pos + 8)[0]
            if target >> 24 == 255:
                index = target & 0xFFFFFF
                key = path.replace("\\", "/").lstrip("/").lower()
                if key in entries:
                    raise SystemExit(f"路径重复：{key}")
                entries[key] = index
            else:
                pending.append((target, path))
    return entries


def entry_bounds(data, table, index):
    offset, size = struct.unpack_from("<II", data, table + index * 8)
    if offset < 64 or size < 4:
        raise SystemExit(f"条目 {index} 边界异常")
    return offset, size


def entry_text(data, table, index):
    offset, size = entry_bounds(data, table, index)
    if data[offset : offset + 4] != BLOCK_HEAD:
        raise SystemExit(f"条目 {index} 块头异常")
    raw = zlib.decompress(veil(data[offset + 4 : offset + size + 4]))
    return raw.decode("gb18030")


def make_block(text):
    """文本 → 条目块字节（4 字节块头 + 混淆后的 zlib 流）。"""
    raw = text.encode("gb18030")
    encoded = veil(zlib.compress(raw))
    return BLOCK_HEAD + encoded


def replace_entries(data, new_texts):
    """与 archive.replace() 同语义：追加新块，改索引表指向，重算 CRC。

    new_texts: {条目号: 新文本}
    """
    tree, table, cks, count = read_header(data)
    addition = bytearray()
    records = {}
    for index in sorted(new_texts):
        block = make_block(new_texts[index])
        payload = block[4:]
        records[index] = (tree + len(addition), len(payload))
        addition += block

    delta = len(addition)
    result = bytearray(data[:tree]) + addition + data[tree:]
    struct.pack_into("<I", result, 40, tree + delta)
    struct.pack_into("<I", result, 44, table + delta)
    struct.pack_into("<I", result, 48, cks + delta)
    for index, (offset, size) in records.items():
        struct.pack_into("<II", result, table + delta + index * 8, offset, size)
        crc = zlib.crc32(bytes(result[offset : offset + size + 4])) & 0xFFFFFFFF
        struct.pack_into("<I", result, cks + delta + index * 8, crc)
    summary = bytes().join(
        bytes(result[cks + delta + i * 8 : cks + delta + i * 8 + 4])
        for i in range(count)
    )
    struct.pack_into("<I", result, 56, zlib.crc32(summary) & 0xFFFFFFFF)
    return bytes(result)


def compact(data):
    """只保留索引表当前指向的块，按条目号重排。"""
    tree, table, cks, count = read_header(data)
    blocks = bytearray()
    new_table = bytearray(count * 8)
    new_cks = bytearray(count * 8)
    for i in range(count):
        offset, size = struct.unpack_from("<II", data, table + i * 8)
        if data[offset : offset + 4] != BLOCK_HEAD:
            raise SystemExit(f"条目 {i} 块头异常")
        block = data[offset : offset + size + 4]
        struct.pack_into("<II", new_table, i * 8, 64 + len(blocks), size)
        blocks += block
        crc = zlib.crc32(block) & 0xFFFFFFFF
        struct.pack_into("<I", new_cks, i * 8, crc)
        # 保留后 4 字节原值，避免无谓抖动
        new_cks[i * 8 + 4 : i * 8 + 8] = data[cks + i * 8 + 4 : cks + i * 8 + 8]

    result = bytearray(data[:64])
    result += blocks
    tree_at = len(result)
    result += data[tree:table]
    table_at = len(result)
    result += new_table
    cks_at = len(result)
    result += new_cks
    struct.pack_into("<I", result, 40, tree_at)
    struct.pack_into("<I", result, 44, table_at)
    struct.pack_into("<I", result, 48, cks_at)
    struct.pack_into("<I", result, 52, count)
    summary = bytes().join(
        bytes(result[cks_at + i * 8 : cks_at + i * 8 + 4]) for i in range(count)
    )
    struct.pack_into("<I", result, 56, zlib.crc32(summary) & 0xFFFFFFFF)
    return bytes(result)


def verify(data):
    tree, table, cks, count = read_header(data)
    bad = 0
    for i in range(count):
        offset, size = struct.unpack_from("<II", data, table + i * 8)
        stored = struct.unpack_from("<I", data, cks + i * 8)[0]
        if zlib.crc32(data[offset : offset + size + 4]) & 0xFFFFFFFF != stored:
            bad += 1
    summary = bytes().join(data[cks + i * 8 : cks + i * 8 + 4] for i in range(count))
    summary_ok = struct.unpack_from("<I", data, 56)[0] == (
        zlib.crc32(summary) & 0xFFFFFFFF
    )
    return {"count": count, "bad_crc": bad, "summary_ok": summary_ok,
            "size": len(data), "ok": bad == 0 and summary_ok}


def main():
    ap = argparse.ArgumentParser(description="往 SGDP config.spf2 的 lua 条目追加内容")
    ap.add_argument("path")
    ap.add_argument("--list", action="store_true", help="列出所有条目（过滤 lua）")
    ap.add_argument("--show", metavar="条目名", help="打印条目文本")
    ap.add_argument("--entry", metavar="条目名", help="要改写的条目（客户端路径写法）")
    ap.add_argument("--append-file", metavar="片段.lua", help="要追加的 lua 片段")
    ap.add_argument("--out", metavar="新文件", help="另存路径")
    ap.add_argument("--in-place", action="store_true", help="原地改写（自动备份）")
    ap.add_argument("--compact", action="store_true", help="写出前先紧凑化")
    ap.add_argument("--verify-only", action="store_true", help="只体检")
    args = ap.parse_args()

    if not os.path.isfile(args.path):
        raise SystemExit(f"找不到 {args.path}")
    data = open(args.path, "rb").read()
    tree, table, cks, count = read_header(data)
    entries = read_tree(data, tree, table)

    if args.verify_only:
        r = verify(data)
        print(f"条目 {r['count']}，CRC 失败 {r['bad_crc']}，汇总CRC "
              f"{'OK' if r['summary_ok'] else 'FAIL'}，{r['size']} 字节")
        return 0 if r["ok"] else 1

    if args.list:
        for key in sorted(entries):
            if args.list and "lua" not in key and not args.list:
                continue
            print(f"  {key}")
        print(f"共 {len(entries)} 条")
        return 0

    if args.show:
        key = args.show.replace("\\", "/").lstrip("/").lower()
        if key not in entries:
            raise SystemExit(f"没有这个条目：{key}")
        sys.stdout.write(entry_text(data, table, entries[key]))
        return 0

    if not args.entry:
        raise SystemExit("需要 --entry（或用 --list / --show）")
    key = args.entry.replace("\\", "/").lstrip("/").lower()
    if key not in entries:
        raise SystemExit(f"没有这个条目：{key}")
    if not args.append_file:
        raise SystemExit("需要 --append-file")
    addition = open(args.append_file, encoding="utf-8").read()

    index = entries[key]
    original = entry_text(data, table, index)
    if addition.strip() and addition.strip() in original:
        raise SystemExit("片段已存在（避免重复追加）；请先手工清理")

    merged = original.rstrip("\r\n") + "\r\n\r\n" + addition.replace("\r\n", "\n").replace("\n", "\r\n").rstrip("\r\n") + "\r\n"
    patched = replace_entries(data, {index: merged})
    if args.compact:
        patched = compact(patched)

    r = verify(patched)
    if not r["ok"]:
        raise SystemExit(f"校验失败：CRC 失败 {r['bad_crc']} 条，汇总 {'FAIL' if not r['summary_ok'] else 'OK'}")

    if args.in_place:
        backup = f"{args.path}.bak-{time.strftime('%Y%m%d-%H%M%S')}"
        shutil.copy2(args.path, backup)
        print(f"已备份 → {backup}")
        dst = args.path
    elif args.out:
        dst = args.out
    else:
        raise SystemExit("请指定 --out 或 --in-place")

    os.makedirs(os.path.dirname(os.path.abspath(dst)) or ".", exist_ok=True)
    open(dst, "wb").write(patched)
    print(f"✅ 已写入 {dst}")
    print(f"   条目 {r['count']}，CRC 全部通过，{len(data)} → {len(patched)} 字节"
          f"（+{len(patched) - len(data)}）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
