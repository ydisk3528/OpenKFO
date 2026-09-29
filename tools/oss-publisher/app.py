"""Windows operator UI. Credentials never enter config files or release packages."""
import json
import os
import queue
import sys
import tempfile
import threading
from pathlib import Path
import tkinter as tk
from tkinter import filedialog, messagebox, scrolledtext, ttk

from publisher import OssStore, announcement_bytes, inspect_zip, publish, error_message, set_release_notes, retain_previous_client, POINTER


class App:
    def __init__(self, root):
        self.root = root
        root.title('OSS 发布工具 1.0.5')
        root.geometry('980x830')
        root.minsize(850, 740)
        self.events = queue.Queue()
        self.cancel = threading.Event()
        self.busy = False
        self.folder = Path(os.environ.get('LOCALAPPDATA', str(Path.home()))) / 'OpenKFO' / 'Publisher'
        saved = {}
        try:
            saved = json.loads((self.folder / 'settings.json').read_text(encoding='utf-8'))
        except (OSError, ValueError):
            pass
        if not isinstance(saved, dict):
            saved = {}
        self.variables = {name: tk.StringVar(value=saved.get(name, default)) for name, default in [
            ('bucket', 'openkfo'), ('region', 'cn-hangzhou'), ('base', 'https://openkfo.oss-cn-hangzhou.aliyuncs.com/')]}
        for name, env in [('access_id', 'OSS_ACCESS_KEY_ID'), ('secret', 'OSS_ACCESS_KEY_SECRET'), ('token', 'OSS_SESSION_TOKEN')]:
            self.variables[name] = tk.StringVar(value=os.environ.get(env, ''))
        self.zip_path = tk.StringVar()
        self.notice_title = tk.StringVar(value='更新公告')
        self.status = tk.StringVar(value='选择 OSS 更新包 ZIP，先检查，再上传发布。')
        style = ttk.Style(root)
        style.theme_use('clam')
        style.configure('.', font=('Microsoft YaHei UI', 10))
        frame = ttk.Frame(root, padding=16)
        frame.pack(fill='both', expand=True)
        ttk.Label(frame, text='OSS 更新发布', font=('Microsoft YaHei UI', 19, 'bold')).pack(anchor='w')
        ttk.Label(frame, text='上传工具仅供发布者使用 · 不会启动游戏或修改服务器').pack(anchor='w', pady=(3, 10))
        settings = ttk.LabelFrame(frame, text='OSS 连接配置', padding=10)
        settings.pack(fill='x')
        settings.columnconfigure(1, weight=1)
        settings.columnconfigure(3, weight=1)
        self.inputs, self.buttons = [], []
        for index, (label, name) in enumerate([('Bucket', 'bucket'), ('地域', 'region'),
                ('AccessKey ID', 'access_id'), ('AccessKey Secret', 'secret'), ('下载根地址', 'base'), ('STS Token（可选）', 'token')]):
            row, col = divmod(index, 2)
            ttk.Label(settings, text=label).grid(row=row, column=col * 2, sticky='w', padx=(0, 8), pady=4)
            entry = ttk.Entry(settings, textvariable=self.variables[name], show='*' if name in ('secret', 'token') else '')
            entry.grid(row=row, column=col * 2 + 1, sticky='ew', padx=(0, 12), pady=4)
            self.inputs.append(entry)
        ttk.Label(settings, text='密钥不保存；下次可重新填写，或通过环境变量提供。').grid(row=3, column=0, columnspan=4, sticky='w', pady=(5, 0))
        tabs = ttk.Notebook(frame)
        tabs.pack(fill='x', pady=12)
        release = ttk.Frame(tabs, padding=12)
        notice = ttk.Frame(tabs, padding=12)
        tabs.add(release, text='发布更新包')
        tabs.add(notice, text='独立公告')
        chooser = ttk.Frame(release)
        chooser.pack(fill='x')
        entry = ttk.Entry(chooser, textvariable=self.zip_path)
        entry.pack(side='left', fill='x', expand=True, padx=(0, 8))
        self.inputs.append(entry)
        self.button(chooser, '选择 ZIP…', self.choose).pack(side='left')
        ttk.Label(release, text='只需选择本次 OSS 更新包；自动继承线上历史文件，同路径以本次为准，无需重复打包旧资源。').pack(anchor='w', pady=8)
        self.package_info = tk.StringVar(value='尚未检查压缩包')
        ttk.Label(release, textvariable=self.package_info, wraplength=840).pack(anchor='w', pady=(0, 10))
        self.edit_notes = tk.BooleanVar(value=False)
        edit = ttk.Checkbutton(release, text='使用下面的版本更新说明（不勾选则沿用包内说明）', variable=self.edit_notes)
        edit.pack(anchor='w')
        self.inputs.append(edit)
        self.release_notes = scrolledtext.ScrolledText(release, height=4, wrap='word', font=('Microsoft YaHei UI', 10))
        self.release_notes.pack(fill='x', pady=6)
        actions = ttk.Frame(release)
        actions.pack(fill='x')
        self.button(actions, '仅检查压缩包', lambda: self.start('inspect')).pack(side='left', padx=(0, 8))
        self.button(actions, '预览合并清单', lambda: self.start('preview')).pack(side='left', padx=(0, 8))
        self.button(actions, '解压并上传发布', lambda: self.start('publish')).pack(side='left', padx=(0, 8))
        self.button(actions, '读取线上版本', lambda: self.start('version')).pack(side='left')
        ttk.Label(release, text='资源全部上传并回读校验后，最后发布 version/version.json。更新包不会覆盖独立公告。').pack(anchor='w', pady=(10, 0))
        title = ttk.Entry(notice, textvariable=self.notice_title)
        title.pack(fill='x')
        self.inputs.append(title)
        self.notice = scrolledtext.ScrolledText(notice, height=5, wrap='word', font=('Microsoft YaHei UI', 10))
        self.notice.pack(fill='x', pady=8)
        notice_actions = ttk.Frame(notice)
        notice_actions.pack(fill='x')
        self.button(notice_actions, '读取线上公告', lambda: self.start('read_notice')).pack(side='left', padx=(0, 8))
        self.button(notice_actions, '上传公告', lambda: self.start('notice')).pack(side='left')
        ttk.Label(notice_actions, text='只更新 announcement.json，不发布软件版本').pack(side='left', padx=12)
        ttk.Label(frame, textvariable=self.status, wraplength=900).pack(anchor='w', pady=(0, 5))
        self.file_progress = ttk.Progressbar(frame, maximum=100)
        self.file_progress.pack(fill='x')
        ttk.Label(frame, text='当前文件上传进度；下方为总体文件进度').pack(anchor='w', pady=4)
        self.total_progress = ttk.Progressbar(frame, maximum=100)
        self.total_progress.pack(fill='x', pady=(0, 8))
        self.log = scrolledtext.ScrolledText(frame, height=8, state='disabled', wrap='word', font=('Microsoft YaHei UI', 9))
        self.log.pack(fill='both', expand=True)
        footer = ttk.Frame(frame)
        footer.pack(fill='x', pady=(8, 0))
        self.stop = ttk.Button(footer, text='取消上传', command=self.cancel_upload, state='disabled')
        self.stop.pack(side='left')
        ttk.Button(footer, text='复制日志', command=self.copy_log).pack(side='right')
        root.protocol('WM_DELETE_WINDOW', self.close)
        root.after(100, self.poll)

    def button(self, parent, text, command):
        button = ttk.Button(parent, text=text, command=command)
        self.buttons.append(button)
        return button

    def choose(self):
        path = filedialog.askopenfilename(filetypes=[('OSS 更新包', '*.zip')])
        if path:
            self.zip_path.set(path)
            self.edit_notes.set(False)
            self.release_notes.delete('1.0', 'end')
            self.package_info.set('已选择，正在读取包内更新说明')
            self.start('inspect')

    def copy_log(self):
        self.root.clipboard_clear()
        self.root.clipboard_append(self.log.get('1.0', 'end-1c'))

    def append(self, text):
        self.log.configure(state='normal')
        self.log.insert('end', text + '\n')
        self.log.see('end')
        self.log.configure(state='disabled')

    def cancel_upload(self):
        self.cancel.set()
        self.stop.configure(state='disabled')
        self.status.set('正在取消；当前网络请求结束后停止，尚未发布的版本入口不会更新。')

    def close(self):
        if self.busy:
            messagebox.showinfo('任务进行中', '请先取消上传并等待任务结束，再关闭窗口。')
            return
        self.root.destroy()

    def start(self, action):
        if self.busy:
            return
        config = {key: value.get().strip() for key, value in self.variables.items()}
        path, title, content = self.zip_path.get(), self.notice_title.get(), self.notice.get('1.0', 'end-1c')
        if action in ('inspect', 'preview', 'publish') and not Path(path).is_file():
            messagebox.showerror('请选择压缩包', '请先选择已有的 OSS 更新包 ZIP。')
            return
        override_notes = self.release_notes.get('1.0', 'end-1c') if self.edit_notes.get() else None
        self.folder.mkdir(parents=True, exist_ok=True)
        (self.folder / 'settings.json').write_text(json.dumps({k: config[k] for k in ('bucket', 'region', 'base')}, ensure_ascii=False, indent=2), encoding='utf-8')
        self.busy = True
        self.cancel.clear()
        for widget in self.inputs + self.buttons:
            widget.configure(state='disabled')
        self.notice.configure(state='disabled')
        self.release_notes.configure(state='disabled')
        self.stop.configure(state='normal' if action in ('inspect', 'publish') else 'disabled')
        self.file_progress['value'] = self.total_progress['value'] = 0
        self.status.set('正在处理…')
        self.append({'inspect': '检查本地 ZIP（不连接 OSS）', 'preview': '读取线上清单并预览合并（不上传）', 'publish': '开始上传发布', 'notice': '上传独立公告', 'read_notice': '读取独立公告', 'version': '读取版本'}[action])

        def notify(kind, value):
            self.events.put((kind, value))

        def work():
            try:
                if action in ('inspect', 'preview', 'publish'):
                    with tempfile.TemporaryDirectory(prefix='OpenKFO-oss-') as temporary:
                        release = inspect_zip(path, temporary, config['base'], self.cancel)
                        if action in ('preview', 'publish') and override_notes is not None:
                            set_release_notes(release, override_notes)
                        if action == 'inspect' and override_notes is None:
                            notify('release_notes', release.notes)
                        notify('package', f'版本：{release.version} · {len(release.keys)} 个资源/清单文件\n{release.notes}')
                        if action == 'publish':
                            store = OssStore(config['bucket'], config['region'], config['access_id'], config['secret'], config['token'])
                            publish(release, store, notify, self.cancel, self.folder / 'backups')
                        elif action == 'preview':
                            store = OssStore(config['bucket'], config['region'], config['access_id'], config['secret'], config['token'])
                            retain_previous_client(release, store, store.get(POINTER), notify)
                        notify('done', {'publish': '发布完成，版本入口已回读确认。', 'preview': '合并预览完成，没有上传。正式发布会重新读取最新线上清单，并校验历史资源。', 'inspect': '压缩包检查通过，未连接 OSS。'}[action])
                else:
                    store = OssStore(config['bucket'], config['region'], config['access_id'], config['secret'], config['token'])
                    if action == 'notice':
                        data = announcement_bytes(title, content)
                        store.put('announcement.json', data)
                        if store.get('announcement.json') != data:
                            raise RuntimeError('公告上传结果未确认，请点击“读取线上公告”检查')
                        notify('done', '公告上传并回读确认完成。')
                    elif action == 'read_notice':
                        data = store.get('announcement.json')
                        notice = json.loads(data) if data else {'title': '', 'content': ''}
                        if not isinstance(notice, dict) or not isinstance(notice.get('title', ''), str) or not isinstance(notice.get('content', ''), str):
                            raise ValueError('线上公告格式无效')
                        notify('notice', notice)
                        notify('done', '公告读取完成。' if data else '线上暂未设置公告。')
                    else:
                        data = store.get('version/version.json')
                        notify('done', f'线上版本：{json.loads(data)["version"]}' if data else '线上暂未发布版本。')
            except Exception as error:
                # Never expose SDK request snapshots, signed headers or credentials.
                text = error_message(error)
                for key in ('access_id', 'secret', 'token'):
                    if config[key]:
                        text = text.replace(config[key], '[已隐藏]')
                notify('error', text)
            finally:
                notify('idle', None)
        threading.Thread(target=work, daemon=True).start()

    def poll(self):
        for _ in range(200):
            try:
                kind, value = self.events.get_nowait()
            except queue.Empty:
                break
            if kind in ('log', 'done', 'error', 'committing'):
                self.append(value)
            if kind in ('done', 'error', 'committing'):
                self.status.set(value)
            if kind == 'committing':
                self.stop.configure(state='disabled')
            elif kind == 'package':
                self.package_info.set(value)
                self.append(value)
            elif kind == 'release_notes':
                self.release_notes.configure(state='normal')
                self.release_notes.delete('1.0', 'end')
                self.release_notes.insert('1.0', value)
                self.release_notes.configure(state='disabled')
            elif kind == 'file':
                index, count, key = value
                self.status.set(f'{index}/{count} · {key}')
                self.total_progress['value'] = 100 * (index - 1) / count
                self.file_progress['value'] = 0
            elif kind == 'progress':
                done, total = value
                self.file_progress['value'] = 100 * done / total if total else 0
            elif kind == 'done':
                self.total_progress['value'] = 100
            elif kind == 'notice':
                self.notice_title.set(value.get('title', ''))
                self.notice.configure(state='normal')
                self.notice.delete('1.0', 'end')
                self.notice.insert('1.0', value.get('content', ''))
            elif kind == 'idle':
                self.release_notes.configure(state='normal')
                self.busy = False
                for widget in self.inputs + self.buttons:
                    widget.configure(state='normal')
                self.notice.configure(state='normal')
                self.stop.configure(state='disabled')
        self.root.after(100, self.poll)


if __name__ == '__main__':
    root = tk.Tk()
    app = App(root)
    if '--ui-smoke-test' in sys.argv:
        # Construct the bundled SDK without making any network request.
        OssStore('openkfo', 'cn-hangzhou', 'test-only', 'test-only')
        root.after(500, root.destroy)
    root.mainloop()
