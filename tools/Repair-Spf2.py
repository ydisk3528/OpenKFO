#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Repair-Spf2.py —— 修复「头部被清零 / 校验区被写坏」的 config.spf2（SGDP 包）。

背景（2026-09-26 实际遇到）：某个第三方打包工具重写出来的 config.spf2 有两个问题——
  1) offset 0..39 被清零（magic + 版本号没了）→ `parseArchive` 直接报「配置包格式错误」；
  2) 校验表区间被写成了**索引表的副本**（存 offset/size 而不是 CRC）→ 即使补回头部，
     `verify()` 也会全条目报「条目 CRC 校验失败」。
两者都只影响「格式头 + 完整性字段」，**数据区是好的**（每个条目都能解压）。所以可以重建。

用法：
  Repair-Spf2.py --check  <文件>                  # 只体检，不写文件
  Repair-Spf2.py --check --orphans <文件>          # 体检 + 列出孤儿数据块
  Repair-Spf2.py --check --max-orphan-ratio 25 <文件>   # 巡检：占比超阈值则退出码 1
  Repair-Spf2.py --out <新文件> <文件>            # 修复并另存（推荐）
  Repair-Spf2.py --in-place <文件>                # 原地修，先备份 <文件>.broken-<时间>

体检除格式之外还会报「孤儿数据块」占比。SGDP 的写入（archive.go 的 replace()）是
**追加式**的：每次改写一个条目就把新块追加到数据区末尾，只改索引表的指向，
**旧副本永远留在文件里**。所以文件只增不减 —— 实测线上 config.spf2 10.6 MB 里有 8.1 MB
（76%）是这种死数据。孤儿块不影响客户端读取（客户端按目录树定位），
但会让体积和载入时间白白膨胀。**要回收请看 Compact-Spf2.py。**
本脚本只重建头部/校验表，**明确不改变文件长度**，解决不了体积问题。

判断依据（SGDP 布局，见 internal/desktop/archive.go）：
  [0,40)   头：0..18 magic "SGDP Package File.\\0" / 19..31 零 / 32..35 = 0x20
              36..39 version = 2 / 40..43 tree / 44..47 table / 48..51 checksums / 52..55 count
  [56,60)  汇总 CRC = crc32(校验表里每条 4 字节 CRC 拼起来)
  [60,64)  固定标记 8f ec 07 1f
  [tree,table)      目录树（名字 → 条目号）
  [table,checksums) 索引：每 8 字节 (offset, size)
  [checksums,len)   校验表：每 8 字节 (crc32, 保留)。crc32 覆盖 data[offset:offset+size+4]，
                    即「4 字节前缀 00 22 00 00 + zlib 流（用 configKey 异或后）」
  边界必须满足 tree <= table < checksums 且 table+count*8 == checksums、checksums+count*8 == len
"""

import argparse
import os
import shutil
import struct
import sys
import time
import zlib

MAGIC = b"SGDP Package File.\x00"
# archive.go 的 configKey：异或混淆用的固定密钥
CONFIG_KEY = b"F48A715746514613BD70EF276119128F"
TAIL = bytes.fromhex("8fec071f")
# 孤儿数据块占比超过这个值就在体检里告警（0.25 = 四分之一是死数据）
ORPHAN_WARN_RATIO = 0.25


def read_meta(data):
    if len(data) < 64:
        raise ValueError("文件太短")
    tree, table, cks, count = struct.unpack_from("<IIII", data, 40)
    if not (64 <= tree <= table < cks):
        raise ValueError(f"区间不合法：tree={tree} table={table} checksums={cks}")
    if table + count * 8 != cks or cks + count * 8 != len(data):
        raise ValueError(
            f"边界不自洽：table+count*8={table + count * 8} checksums={cks} "
            f"checksums+count*8={cks + count * 8} 文件={len(data)}"
        )
    if not (1 <= count <= 10000):
        raise ValueError(f"条目数异常：{count}")
    return tree, table, cks, count


def decompress_entry(data, offset, size):
    if struct.unpack_from("<I", data, offset)[0] != 0x2200:
        return None
    buf = bytearray(data[offset + 4 : offset + size + 4])
    for i in range(len(buf)):
        buf[i] ^= 255 ^ CONFIG_KEY[i % len(CONFIG_KEY)]
    try:
        return zlib.decompress(bytes(buf))
    except zlib.error:
        return None


# ---------------------------------------------------------------------------
# 孤儿数据块体检
#
# 背景：archive.go 的 replace() 是「追加式」写入 —— 老数据区原样照搬、新块追加在后面、
# 只把索引表指向新块。于是每改写一个条目，它的旧副本就永久留在文件里变成死数据。
# 症状：文件只增不减（实测 10.6 MB 的文件里 8.1 MB 是孤儿）。
#
# 判定方法：块头只有 4 字节 0x2200，**不含长度**，所以要靠 zlib 的 eof/unused_data
# 反推压缩流长度。扫描整个数据区 [64, tree) 拿到「物理块集合」，
# 减去「索引表引用的 offset 集合」，剩下的就是孤儿。
# ---------------------------------------------------------------------------

# 32 张 256 字节翻译表：块内下标 % 32 == p 的字节用第 p 张。
# 这样揭异或可以走 bytes.translate（C 实现），比逐字节 Python 循环快两个数量级。
XOR_TABLES = tuple(
    bytes(c ^ (255 ^ CONFIG_KEY[p % len(CONFIG_KEY)]) for c in range(256))
    for p in range(32)
)


def unveil(body):
    """揭掉 configKey 异或。body 必须是「从块内下标 0 开始」的连续字节。"""
    out = bytearray(len(body))
    for p in range(32):
        out[p::32] = body[p::32].translate(XOR_TABLES[p])
    return bytes(out)


def block_length(data, offset, limit=1 << 26):
    """返回该物理块的总长度（含 4 字节块头）；不是合法块则返回 None。"""
    if offset + 4 > len(data) or struct.unpack_from("<I", data, offset)[0] != 0x2200:
        return None
    reader = zlib.decompressobj()
    consumed = 0
    while consumed < limit:
        # 65536 是 32 的倍数，所以每轮开头都保持在块内 32 字节对齐线上。
        chunk = data[offset + 4 + consumed : offset + 4 + consumed + 65536]
        if not chunk:
            return None
        try:
            reader.decompress(unveil(chunk))
        except zlib.error:
            return None
        consumed += len(chunk)
        if reader.eof:
            return 4 + consumed - len(reader.unused_data)
    return None


def scan_blocks(data, tree):
    """扫描数据区 [64, tree)，返回所有物理块 [(offset, 总长度含块头), ...]。"""
    blocks = []
    pos = 64
    while True:
        found = data.find(b"\x00\x22\x00\x00", pos, tree)
        if found < 0:
            break
        length = block_length(data, found)
        if length is None:
            pos = found + 1
            continue
        blocks.append((found, length))
        pos = found + 4
    return blocks


def orphan_stats(data, tree, table, count):
    """物理块 vs 索引表引用，算出孤儿块规模。"""
    blocks = scan_blocks(data, tree)
    live_offsets = set()
    for i in range(count):
        offset = struct.unpack_from("<II", data, table + i * 8)[0]
        live_offsets.add(offset)
    orphans = [(o, n) for o, n in blocks if o not in live_offsets]
    return {
        "blocks": len(blocks),
        "bytes": sum(n for _, n in blocks),
        "orphan_blocks": len(orphans),
        "orphan_bytes": sum(n for _, n in orphans),
        "orphan_offsets": {o for o, _ in orphans},
        "orphans": orphans,
    }


def inspect(path):
    data = open(path, "rb").read()
    tree, table, cks, count = read_meta(data)
    magic_ok = data[: len(MAGIC)] == MAGIC
    version = struct.unpack_from("<I", data, 36)[0]
    tail_ok = data[60:64] == TAIL
    bad_crc, bad_zip, zero_scan = 0, 0, 0
    crc_table_looks_like_index = 0
    for i in range(count):
        offset, size = struct.unpack_from("<II", data, table + i * 8)
        stored = struct.unpack_from("<I", data, cks + i * 8)[0]
        if offset + size + 4 > len(data):
            bad_crc += 1
            continue
        if zlib.crc32(data[offset : offset + size + 4]) & 0xFFFFFFFF != stored:
            bad_crc += 1
        if stored == offset or stored == size:
            crc_table_looks_like_index += 1
        if decompress_entry(data, offset, size) is None:
            bad_zip += 1
        zero_scan += 1
    summary = bytes().join(data[cks + i * 8 : cks + i * 8 + 4] for i in range(count))
    summary_ok = struct.unpack_from("<I", data, 56)[0] == (
        zlib.crc32(summary) & 0xFFFFFFFF
    )
    orphans = orphan_stats(data, tree, table, count)
    return {
        "path": path,
        "size": len(data),
        "count": count,
        "magic_ok": magic_ok,
        "version": version,
        "tail_ok": tail_ok,
        "summary_ok": summary_ok,
        "bad_crc": bad_crc,
        "bad_zip": bad_zip,
        "crc_looks_like_index": crc_table_looks_like_index,
        "ok": magic_ok and version == 2 and summary_ok and bad_crc == 0,
        **orphans,
        "orphan_ratio": orphans["orphan_bytes"] / len(data) if data else 0.0,
        "tree": tree,
        "table": table,
        "checksums": cks,
    }


def compact_size(r):
    """重打包后的理论体积：头 + 真内容 + 目录树 + 索引表 + 校验表。"""
    live = r["bytes"] - r["orphan_bytes"]
    return 64 + live + (r["table"] - r["tree"]) + r["count"] * 16


def report(r):
    print(f"文件：{r['path']}")
    print(f"  大小 {r['size']} 字节，条目 {r['count']}")
    print(f"  头部 magic {'✅' if r['magic_ok'] else '❌ 被清零'}   version={r['version']}"
          f"{'✅' if r['version'] == 2 else ' ❌ 应为 2'}   尾标记 {'✅' if r['tail_ok'] else '❌'}   汇总 CRC {'✅' if r['summary_ok'] else '❌'}")
    print(f"  条目 CRC 失败 {r['bad_crc']}/{r['count']}"
          + ("（校验表疑似被写成索引副本）" if r["crc_looks_like_index"] > r["count"] // 2 else ""))
    print(f"  条目解压失败 {r['bad_zip']}/{r['count']}")
    ratio = r["orphan_ratio"]
    print(f"  孤儿数据块 {r['orphan_blocks']} 个 / {r['orphan_bytes']} 字节"
          f"（占全文件 {ratio * 100:.1f}%；物理块共 {r['blocks']} 个，"
          f"真内容 {r['bytes'] - r['orphan_bytes']} 字节）")
    if ratio >= ORPHAN_WARN_RATIO:
        print(f"  ⚠ 死数据偏多：每改写一次条目就会留下一份旧副本、永不回收。")
        print(f"    重打包可压到约 {compact_size(r)} 字节"
              f"（回收 {r['orphan_bytes']} 字节，-{ratio * 100:.0f}%）；"
              f"用 Compact-Spf2.py --in-place 处理")
    print(f"  结论：{'✅ 可直接使用' if r['ok'] else '❌ 需要修复'}")


def repair(src, dst):
    data = bytearray(open(src, "rb").read())
    tree, table, cks, count = read_meta(data)

    # 数据必须先自证清白：全部能解压，否则不敢动它
    bad = [i for i in range(count)
           if decompress_entry(data, *struct.unpack_from("<II", data, table + i * 8)) is None]
    if bad:
        raise SystemExit(f"拒绝修复：{len(bad)} 个条目解压失败（数据本身已坏，例：条目 {bad[:5]}）")

    before = bytes(data)
    data[0 : len(MAGIC)] = MAGIC
    data[len(MAGIC) : 32] = b"\x00" * (32 - len(MAGIC))
    struct.pack_into("<I", data, 32, 0x20)
    struct.pack_into("<I", data, 36, 2)
    for i in range(count):
        offset, size = struct.unpack_from("<II", data, table + i * 8)
        struct.pack_into(
            "<I", data, cks + i * 8,
            zlib.crc32(bytes(data[offset : offset + size + 4])) & 0xFFFFFFFF,
        )
    summary = bytes().join(bytes(data[cks + i * 8 : cks + i * 8 + 4]) for i in range(count))
    struct.pack_into("<I", data, 56, zlib.crc32(summary) & 0xFFFFFFFF)
    data[60:64] = TAIL
    if len(data) != len(before):
        raise SystemExit("内部错误：修复改变了文件长度")
    open(dst, "wb").write(data)
    return dst


def list_orphans(r, limit=40):
    """列出孤儿块，按体积倒序，顺带解压出开头几十字节好认人。"""
    data = open(r["path"], "rb").read()
    rows = []
    for offset, length in r["orphans"]:
        text = ""
        try:
            text = decompress_entry(data, offset, length - 4)[:70].decode(
                "gb18030", "replace"
            )
        except Exception:
            pass
        rows.append(
            (length, offset, text.replace("\n", " ").replace("\r", " ").replace("\t", " "))
        )
    rows.sort(reverse=True)
    print(f"\n孤儿数据块 {len(rows)} 个（按体积倒序，最多显示 {limit} 条）：")
    for length, offset, text in rows[:limit]:
        print(f"  off={offset:<10} {length:>8} 字节  {text[:62]}")
    if len(rows) > limit:
        print(f"  …还有 {len(rows) - limit} 个未显示")


def main():
    ap = argparse.ArgumentParser(description="修复被改坏的 SGDP config.spf2")
    ap.add_argument("path")
    ap.add_argument("--out")
    ap.add_argument("--in-place", action="store_true")
    ap.add_argument("--check", action="store_true")
    ap.add_argument(
        "--orphans",
        action="store_true",
        help="列出孤儿数据块（谁的历史副本），最多 40 条",
    )
    ap.add_argument(
        "--max-orphan-ratio",
        type=float,
        default=None,
        metavar="PCT",
        help="孤儿占比超过 PCT%% 时以退出码 1 结束（供巡检用）",
    )
    args = ap.parse_args()

    if not os.path.isfile(args.path):
        raise SystemExit(f"找不到 {args.path}")

    r = inspect(args.path)
    report(r)

    if args.orphans:
        list_orphans(r)

    if args.max_orphan_ratio is not None and r["orphan_ratio"] * 100 > args.max_orphan_ratio:
        print(f"\n❌ 孤儿占比 {r['orphan_ratio'] * 100:.1f}% 超过阈值 {args.max_orphan_ratio}%")
        return 1

    if args.check:
        return 0 if r["ok"] else 1
    if r["ok"] and not (args.out or args.in_place):
        print("无需修复。")
        return 0

    if args.in_place:
        backup = f"{args.path}.broken-{time.strftime('%Y%m%d-%H%M%S')}"
        shutil.copy2(args.path, backup)
        print(f"已备份原文件 → {backup}")
        dst = args.path
    elif args.out:
        dst = args.out
    else:
        raise SystemExit("请指定 --out <新文件> 或 --in-place")

    repair(args.path, dst)
    print()
    print("修复后：")
    report(inspect(dst))
    return 0


if __name__ == "__main__":
    sys.exit(main())
