#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Compact-Spf2.py —— 重打包 SGDP config.spf2，回收「孤儿数据块」。

背景（2026-09-27 定位）：
  `internal/desktop/archive.go` 的 `replace()` 是**追加式**写入：

      result = data[:tree] + 新块 + data[tree:]

  老数据区原样照搬、新块追加在后面、只把索引表指向新块。于是**每改写一个条目，
  它的旧副本就永久留在文件里**，文件只增不减。实测线上 `client/Data/config.spf2`：
  10,605,791 字节里有 8,102,658 字节（76%）是这种孤儿，真内容只有 2.46 MB。
  典型指纹：`animation/2001.xml` 在文件里躺着 11 个副本，解压体积单调递增
  3,764,738 → 3,816,101，最新版被挤到最大偏移处。

本脚本按**索引表**重建数据区：每个条目只搬一次（搬的就是当前生效的那份块），
所以重打包**不改变任何条目的内容**，只丢掉没人引用的死数据。
客户端按目录树定位条目，紧凑包完全合法（原始发行版就是紧凑的）。

用法：
  Compact-Spf2.py --check <文件>            # 只报能回收多少，不写
  Compact-Spf2.py --out <新文件> <文件>      # 重打包另存（推荐）
  Compact-Spf2.py --in-place <文件>         # 原地重打包，先备份 <文件>.packed-<时间>

安全措施：
  * 动手前逐条目自证：块头 0x2200 正确、zlib 能解开；任何一条失败就拒绝重打包；
  * 落盘前逐条目**字节比对**新旧块，并**解压内容比对**，全等才写；
  * 断言「新文件不得比原文件大」；
  * `--in-place` 先复制一份带时间戳的备份；
  * 原子写（先写 .tmp 再 os.replace）。

SGDP 布局（见 internal/desktop/archive.go）：
  [0,64)            头：magic "SGDP Package File.\\0" / 36..39 version / 40..43 tree
                        / 44..47 table / 48..51 checksums / 52..55 count
                        / 56..59 汇总 CRC / 60..63 固定标记 8f ec 07 1f
  [64,tree)         数据区：每块 = 4 字节 0x2200 + zlib 流（用 configKey 异或后）
  [tree,table)      目录树（文件名 → 条目号）
  [table,checksums) 索引表：每条 8 字节 (offset, size)，size 不含那 4 字节头
  [checksums,len)   校验表：每条 8 字节 (crc32, 保留)，crc32 覆盖整块含头
  边界必须满足 table + count*8 == checksums 且 checksums + count*8 == len
"""

import argparse
import os
import shutil
import struct
import sys
import time
import zlib

MAGIC = b"SGDP Package File.\x00"
CONFIG_KEY = b"F48A715746514613BD70EF276119128F"
TAIL = bytes.fromhex("8fec071f")

# 32 张 256 字节翻译表：块内下标 % 32 == p 的字节用第 p 张。
# 揭异或走 bytes.translate（C 实现），比逐字节 Python 循环快两个数量级。
XOR_TABLES = tuple(
    bytes(c ^ (255 ^ CONFIG_KEY[p % len(CONFIG_KEY)]) for c in range(256))
    for p in range(32)
)


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


def unveil(body):
    """揭掉 configKey 异或。body 必须从块内下标 0 开始、且是连续字节。"""
    out = bytearray(len(body))
    for p in range(32):
        out[p::32] = body[p::32].translate(XOR_TABLES[p])
    return bytes(out)


def inflate_block(data, offset, size):
    """解压一个条目；失败返回 None。size 不含 4 字节块头。"""
    if offset + size + 4 > len(data):
        return None
    if struct.unpack_from("<I", data, offset)[0] != 0x2200:
        return None
    try:
        return zlib.decompress(unveil(data[offset + 4 : offset + size + 4]))
    except zlib.error:
        return None


def _selfcheck(data):
    """等价于 Repair-Spf2.py 的 inspect().ok：格式头 + 全条目 CRC + 全条目可解压。"""
    tree, table, cks, count = read_meta(data)
    problems = []
    if data[: len(MAGIC)] != MAGIC:
        problems.append("magic 不对")
    if struct.unpack_from("<I", data, 36)[0] != 2:
        problems.append("version 不是 2")
    if data[60:64] != TAIL:
        problems.append("尾标记不对")
    bad_crc = 0
    for i in range(count):
        offset, size = struct.unpack_from("<II", data, table + i * 8)
        if offset + size + 4 > tree:
            bad_crc += 1
            continue
        if zlib.crc32(data[offset : offset + size + 4]) & 0xFFFFFFFF != struct.unpack_from(
            "<I", data, cks + i * 8
        )[0]:
            bad_crc += 1
    if bad_crc:
        problems.append(f"条目 CRC 失败 {bad_crc}/{count}")
    bad_zip = sum(
        1
        for i in range(count)
        if inflate_block(data, *struct.unpack_from("<II", data, table + i * 8)) is None
    )
    if bad_zip:
        problems.append(f"条目解压失败 {bad_zip}/{count}")
    summary = bytes().join(data[cks + i * 8 : cks + i * 8 + 4] for i in range(count))
    if struct.unpack_from("<I", data, 56)[0] != zlib.crc32(summary) & 0xFFFFFFFF:
        problems.append("汇总 CRC 不对")
    return problems


def block_totals(data):
    """数据区里物理块的总字节数（含块头），用来对比重打包前后的占用。"""
    tree, table, cks, count = read_meta(data)
    total = 0
    width = 0
    for i in range(count):
        offset, size = struct.unpack_from("<II", data, table + i * 8)
        total += size + 4
    width = tree - 64
    return total, width


def build_compact(data):
    """按索引表重建数据区，返回新文件字节。任何一处自证失败就抛错，绝不半成品落盘。"""
    tree, table, cks, count = read_meta(data)

    # 1) 逐条目自证：边界、块头、可解压
    plans = []
    for i in range(count):
        offset, size = struct.unpack_from("<II", data, table + i * 8)
        if offset < 64 or size < 4 or offset + size + 4 > tree:
            raise SystemExit(f"拒绝重打包：条目 {i} 边界错误 offset={offset} size={size}")
        if struct.unpack_from("<I", data, offset)[0] != 0x2200:
            raise SystemExit(f"拒绝重打包：条目 {i} 块标志不是 0x2200")
        if inflate_block(data, offset, size) is None:
            raise SystemExit(f"拒绝重打包：条目 {i} 解压失败（数据本身已坏）")
        plans.append((offset, size))

    # 2) 按条目号顺序紧凑排布，每条只搬一次
    tree_bytes = bytes(data[tree:table])
    payload = bytearray()
    new_index = bytearray(count * 8)
    new_crc = bytearray(count * 8)
    for i, (offset, size) in enumerate(plans):
        block = bytes(data[offset : offset + size + 4])
        new_offset = 64 + len(payload)
        payload += block
        struct.pack_into("<II", new_index, i * 8, new_offset, size)
        crc = zlib.crc32(block) & 0xFFFFFFFF
        # 保留那 4 字节「保留位」，与原件保持一致，免得引入没必要的差异
        reserved = bytes(data[cks + i * 8 + 4 : cks + i * 8 + 8])
        struct.pack_into("<II", new_crc, i * 8, crc, struct.unpack("<I", reserved)[0])

    # 3) 拼装
    head = bytearray(data[:64])
    new_tree = 64 + len(payload)
    new_table = new_tree + len(tree_bytes)
    new_cks = new_table + count * 8
    struct.pack_into("<I", head, 36, 2)
    struct.pack_into("<I", head, 40, new_tree)
    struct.pack_into("<I", head, 44, new_table)
    struct.pack_into("<I", head, 48, new_cks)
    struct.pack_into("<I", head, 52, count)
    summary = bytes().join(bytes(new_crc[i * 8 : i * 8 + 4]) for i in range(count))
    struct.pack_into("<I", head, 56, zlib.crc32(summary) & 0xFFFFFFFF)
    head[60:64] = TAIL

    return bytes(head) + bytes(payload) + tree_bytes + bytes(new_index) + bytes(new_crc)


def verify_compact(old, new):
    """落盘前的硬校验：逐条目字节一致 + 解压内容一致 + 不得变大 + 格式健康。"""
    problems = []
    if len(new) > len(old):
        problems.append(f"重打包后反而变大：{len(new)} > {len(old)}")
    otree, otable, ocks, ocount = read_meta(old)
    ntree, ntable, ncks, ncount = read_meta(new)
    if ncount != ocount:
        problems.append(f"条目数变了：{ncount} != {ocount}")
    same = 0
    for i in range(min(ocount, ncount)):
        ooff, osize = struct.unpack_from("<II", old, otable + i * 8)
        noff, nsize = struct.unpack_from("<II", new, ntable + i * 8)
        old_block = old[ooff : ooff + osize + 4]
        new_block = new[noff : noff + nsize + 4]
        if old_block != new_block:
            problems.append(f"条目 {i} 的块字节不一致")
            continue
        if inflate_block(old, ooff, osize) != inflate_block(new, noff, nsize):
            problems.append(f"条目 {i} 解压内容不一致")
            continue
        same += 1
    problems.extend(_selfcheck(new))
    return same, problems


def report(src, data):
    size = len(data)
    live, width = block_totals(data)
    orphans = width - live
    ratio = orphans / size if size else 0.0
    print(f"文件：{src}")
    print(f"  大小 {size} 字节，数据区 {width} 字节，条目真内容 {live} 字节")
    print(f"  孤儿数据块占用 {orphans} 字节（占全文件 {ratio * 100:.1f}%）")
    if orphans <= 0:
        print("  已经足够紧凑，无需重打包。")
    else:
        print(f"  重打包可回收约 {orphans} 字节（-{ratio * 100:.0f}%）")
    return orphans


def write_atomic(path, data):
    tmp = f"{path}.tmp"
    with open(tmp, "wb") as stream:
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(tmp, path)


def run(src, dst):
    old = open(src, "rb").read()
    before = report(src, old)
    if before <= 0:
        # 已经是紧凑包：一个字节都不写，避免对生产文件做无意义的改写
        print("\n  已经是紧凑包，未做任何写入。")
        return 0

    new = build_compact(old)
    same, problems = verify_compact(old, new)
    print(f"\n  校验：逐条目字节+解压内容一致 {same} 条")
    if problems:
        for p in problems:
            print(f"    ❌ {p}")
        raise SystemExit("校验未通过，未写入任何文件")
    print("    ✅ 全部通过（含格式头、全条目 CRC、汇总 CRC）")
    print(f"  体积：{len(old)} → {len(new)} 字节"
          f"（{len(new) - len(old):+d}，回收 {len(old) - len(new)}）")

    write_atomic(dst, new)
    print(f"  已写入：{dst}")
    on_disk = open(dst, "rb").read()
    left = _selfcheck(on_disk)
    print(f"  落盘复检：{'✅ 通过' if not left else '❌ ' + '；'.join(left)}")
    if left:
        raise SystemExit("落盘文件复检失败，请勿使用")
    return before


def main():
    ap = argparse.ArgumentParser(description="重打包 SGDP config.spf2，回收孤儿数据块")
    ap.add_argument("path")
    ap.add_argument("--out", metavar="新文件", help="重打包另存（推荐）")
    ap.add_argument("--in-place", action="store_true", help="原地重打包，先备份")
    ap.add_argument("--check", action="store_true", help="只报能回收多少，不写文件")
    args = ap.parse_args()

    if not os.path.isfile(args.path):
        raise SystemExit(f"找不到 {args.path}")

    if args.check:
        report(args.path, open(args.path, "rb").read())
        return 0

    if args.in_place:
        backup = f"{args.path}.packed-{time.strftime('%Y%m%d-%H%M%S')}"
        shutil.copy2(args.path, backup)
        print(f"已备份原文件 → {backup}\n")
        run(backup, args.path)
    elif args.out:
        run(args.path, args.out)
    else:
        raise SystemExit("请指定 --out <新文件> 或 --in-place，或加 --check 只看体检")
    return 0


if __name__ == "__main__":
    sys.exit(main())
