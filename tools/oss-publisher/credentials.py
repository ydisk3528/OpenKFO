"""Windows DPAPI vault, tied to the current Windows user. Never bundled."""
import ctypes
from ctypes import wintypes
import json
import os


class Blob(ctypes.Structure):
    _fields_ = [('size', wintypes.DWORD), ('data', ctypes.POINTER(ctypes.c_ubyte))]


def crypt(data, decrypt=False):
    if os.name != 'nt':
        raise OSError('密钥自动保存仅支持 Windows')
    buffer = ctypes.create_string_buffer(data)
    source = Blob(len(data), ctypes.cast(buffer, ctypes.POINTER(ctypes.c_ubyte)))
    output = Blob()
    dll = ctypes.WinDLL('crypt32', use_last_error=True)
    func = dll.CryptUnprotectData if decrypt else dll.CryptProtectData
    if not func(ctypes.byref(source), None, None, None, None, 1, ctypes.byref(output)):
        raise ctypes.WinError(ctypes.get_last_error())
    try:
        return ctypes.string_at(output.data, output.size)
    finally:
        kernel = ctypes.WinDLL('kernel32')
        kernel.LocalFree.argtypes = [ctypes.c_void_p]
        kernel.LocalFree(output.data)


def load(folder):
    path = folder / 'credentials.dpapi'
    if not path.exists():
        return {}
    return json.loads(crypt(path.read_bytes(), decrypt=True))


def save(folder, values):
    folder.mkdir(parents=True, exist_ok=True)
    path = folder / 'credentials.dpapi'
    path.with_suffix('.next').write_bytes(crypt(json.dumps(values).encode()))
    path.with_suffix('.next').replace(path)
