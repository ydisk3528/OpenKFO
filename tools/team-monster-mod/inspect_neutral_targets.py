"""Read-only target-filter snapshot for the supported gfxz.dat (no remote calls)."""
import argparse
import ctypes as C
import hashlib
import json
import struct

EXPECTED = '1b7c8676e778c7bd47f55184f927338da3cf70c6aa5f0beb59869ea0f0ac9d4e'


def snapshot(pid):
    k = C.WinDLL('kernel32', use_last_error=True)
    k.OpenProcess.restype = C.c_void_p
    k.ReadProcessMemory.argtypes = [C.c_void_p, C.c_void_p, C.c_void_p, C.c_size_t, C.c_void_p]
    k.QueryFullProcessImageNameW.argtypes = [C.c_void_p, C.c_ulong, C.c_wchar_p, C.POINTER(C.c_ulong)]
    k.CloseHandle.argtypes = [C.c_void_p]
    h = k.OpenProcess(0x1010, False, pid)
    if not h:
        raise C.WinError(C.get_last_error())
    try:
        path = C.create_unicode_buffer(32768)
        size = C.c_ulong(len(path))
        if not k.QueryFullProcessImageNameW(h, 0, path, C.byref(size)):
            raise C.WinError(C.get_last_error())
        with open(path.value, 'rb') as f:
            if hashlib.file_digest(f, 'sha256').hexdigest() != EXPECTED:
                raise ValueError('Unsupported client hash')

        def read(addr, fmt='<I'):
            n = struct.calcsize(fmt)
            buf = C.create_string_buffer(n)
            got = C.c_size_t()
            if not k.ReadProcessMemory(h, addr, buf, n, C.byref(got)) or got.value != n:
                raise OSError(f'Read failed at {addr:#x}; scene may have changed')
            values = struct.unpack(fmt, buf.raw)
            return values[0] if len(values) == 1 else values

        # These absolute globals are version-specific; PE load base is checked too.
        if read(0x400000, '<H') != 0x5a4d:
            raise ValueError('Unexpected module base')
        manager, scene = read(0x17c8708), read(0x17c8710)
        phase = read(scene + 0x78)
        if phase != 4:
            raise ValueError('Not in a battle scene')
        room, serial = read(manager + 0x311), read(manager + 0x315)
        own = read(read(0x17c86fc) + 0x18, '<Q')
        clock = read(0x17c8690)
        head = read(manager + 0x2f0)
        pending, seen, actors = [read(head + 4)], set(), []
        while pending:
            node = pending.pop()
            if not node or node == head or node in seen:
                continue
            seen.add(node)
            if len(seen) > 128:
                raise ValueError('Actor tree changed or exceeds expected bound')
            if read(node + 21, '<B'):
                continue
            left, _, right, slot, actor = read(node, '<5I')
            pending.extend((left, right))
            if slot >= 40 or not actor:
                continue
            uid = read(actor + 0xc90, '<Q')
            stamp = read(actor + 0x178c)
            actors.append(dict(slot=slot, uid=uid, own=uid == own,
                               team=read(actor + 0x28, '<i'),
                               position=read(actor + 0xd10, '<3f'),
                               controller=read(actor + 0x1b74),
                               emitter=read(actor + 0xdd0),
                               visibility_field=read(actor + 0xcfc),
                               invincibility_field=read(actor + 0x177c, '<f'),
                               age_ms=(clock - stamp) & 0xffffffff,
                               ai_age_excludes=((clock - stamp) & 0xffffffff) > 15000))
        if (read(0x17c8710) != scene or read(scene + 0x78) != 4 or
                read(manager + 0x315) != serial):
            raise ValueError('Scene changed during snapshot; discard result')
        return dict(pid=pid, room=room, serial=serial, own_uid=own,
                    actors=sorted(actors, key=lambda a: a['slot']),
                    note='Read-only, non-atomic snapshot; HP and actual Lua target are not sampled')
    finally:
        k.CloseHandle(h)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('pid', type=int)
    args = parser.parse_args()
    print(json.dumps(snapshot(args.pid), ensure_ascii=False, indent=2))
