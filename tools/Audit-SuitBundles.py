"""Build reviewed ordinary suit mappings without replacing client configuration."""
import argparse
import json
import re
import sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from server.kk_local.maps import ClientConfig
from server.kk_local.weapon_admin import raw_entry

def audit(path):
    archive = ClientConfig(path)
    rows = [line.split('\t') for line in raw_entry(archive, 'item.txt').decode('gbk').splitlines()]
    items = {int(r[1]): r for r in rows if len(r) > 53 and r[0].isdigit() and r[1].isdigit()}
    bundles, report = {}, []
    for key, row in sorted(items.items()):
        if row[0] != '18':
            continue
        match = re.search(r'(?:cha|body)_(\d+)_(\d+)', row[9], re.I)
        # These two packages have a package-only effect binding, not a complete
        # set of equivalent part definitions. Do not silently lose their effect.
        if row[53] not in ('#', '', '0'):
            report.append(dict(id=key, name=row[3], status='需单独处理套装特效', effect=row[53]))
            continue
        if not match:
            raise ValueError(f'无法识别套装部件系列：{key}')
        family = int(match[1])*1000 + int(match[2])
        parts = [kind*10000+family for kind in range(12, 18) if kind*10000+family in items]
        if not {12, 14, 16} <= {p//10000 for p in parts}:
            raise ValueError(f'套装缺少主要部件：{key}')
        # The full-body model shared by the parts confirms the family even when
        # localized item names or icon filenames contain historical typos.
        models = {items[p][7].casefold() for p in parts}
        mixed_lace = key == 180034 and parts == [122249, 142249, 162249, 172249] and models == {'cha_02_249.dff', 'cha_02_86.dff', 'cha_02_167.dff'}
        if (len(models) != 1 and not mixed_lace) or '#' in models:
            raise ValueError(f'套装部件模型不一致：{key}: {models}')
        bundles[str(key)] = parts
        report.append(dict(id=key, name=row[3], status='已核对', parts=[dict(id=p, name=items[p][3]) for p in parts]))
    return bundles, report

if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('config', type=Path)
    parser.add_argument('output', type=Path)
    args = parser.parse_args()
    bundles, report = audit(args.config)
    args.output.mkdir(parents=True, exist_ok=True)
    (args.output/'suit-bundles.json').write_text(json.dumps({'suit_bundles':bundles}, ensure_ascii=False, indent=2), encoding='utf-8')
    (args.output/'audit.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    print(f'checked={len(report)} mapped={len(bundles)} special={len(report)-len(bundles)}')
