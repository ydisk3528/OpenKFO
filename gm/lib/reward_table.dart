import 'dart:convert';

const rewardColumns = <String, String>{
  'level': '等级',
  'next_experience': '升级所需经验',
  'win_experience': '胜利经验',
  'win_gold': '胜利金币',
  'loss_experience': '失败经验',
  'loss_gold': '失败金币',
  'draw_experience': '平局经验',
  'draw_gold': '平局金币',
};
List<Map<String, int>> rewardRows(Map<String, dynamic> rules) {
  final raw = rules['levels'] as List?;
  return List.generate(
    200,
    (i) => {
      for (final key in rewardColumns.keys)
        key: raw != null && i < raw.length
            ? (raw[i][key] as num).toInt()
            : key == 'level'
            ? i + 1
            : (rules[key] as num?)?.toInt() ?? 0,
    },
  );
}

void validateRewardRows(List<Map<String, int>> rows, {bool growth = false}) {
  if (rows.length != 200) throw const FormatException('必须有 200 行');
  for (var i = 0; i < 200; i++) {
    final row = rows[i];
    if (row['level'] != i + 1) {
      throw const FormatException('等级必须依次为 1–200，不可缺失或重复');
    }
    for (final key in rewardColumns.keys.skip(1)) {
      final n = row[key];
      if (n == null ||
          n < 0 ||
          n > (key == 'next_experience' ? 2147483647 : 1000000)) {
        throw FormatException('第 ${i + 1} 级 ${rewardColumns[key]} 数值超出范围');
      }
    }
    if (i == 199 && row['next_experience'] != 0) {
      throw const FormatException('200 级已满级，升级经验必须为 0');
    }
    if (growth && i < 199 && row['next_experience'] == 0) {
      throw const FormatException('启用升级前需填写 1–199 级升级经验');
    }
  }
}

String rewardsToCsv(List<Map<String, int>> rows) {
  validateRewardRows(rows);
  return '\uFEFF${rewardColumns.keys.join(',')}\r\n${rows.map((r) => rewardColumns.keys.map((k) => r[k]).join(',')).join('\r\n')}\r\n';
}

List<Map<String, int>> rewardsFromCsv(String text) {
  // Numeric cells only. Accept Excel's BOM, quoted numeric cells and CRLF.
  final lines = text.replaceFirst('\uFEFF', '').trim().split(RegExp(r'\r?\n'));
  List<String> cells(String s) => s
      .split(',')
      .map((v) => v.trim().replaceAll(RegExp(r'^"|"$'), ''))
      .toList();
  final header = cells(lines.first);
  if (header.join(',') != rewardColumns.keys.join(',') &&
      header.join(',') != rewardColumns.values.join(',')) {
    throw const FormatException('CSV 表头不匹配，请先导出模板');
  }
  final rows = <Map<String, int>>[];
  for (final line in lines.skip(1)) {
    final values = cells(line);
    if (values.length != 8) throw const FormatException('每行必须有 8 列');
    rows.add({
      for (var i = 0; i < 8; i++)
        rewardColumns.keys.elementAt(i): int.parse(values[i]),
    });
  }
  validateRewardRows(rows);
  return rows;
}

List<String> rewardDiff(
  Map<String, dynamic> source,
  Map<String, dynamic> target,
) {
  final a = rewardRows(source), b = rewardRows(target), result = <String>[];
  if (jsonEncode(source['stage_rewards'] ?? []) !=
      jsonEncode(target['stage_rewards'] ?? [])) {
    result.add(
      '关卡奖励：${jsonEncode(target['stage_rewards'] ?? [])} → ${jsonEncode(source['stage_rewards'] ?? [])}',
    );
  }
  if (jsonEncode(source['level_gifts'] ?? []) !=
      jsonEncode(target['level_gifts'] ?? [])) {
    result.add(
      '升级礼包：${jsonEncode(target['level_gifts'] ?? [])} → ${jsonEncode(source['level_gifts'] ?? [])}',
    );
  }
  if (jsonEncode(source['tutorial_reward'] ?? {}) !=
      jsonEncode(target['tutorial_reward'] ?? {})) {
    result.add(
      '新手引导奖励：${jsonEncode(target['tutorial_reward'] ?? {})} → ${jsonEncode(source['tutorial_reward'] ?? {})}',
    );
  }
  final sourceDrops = source['drops'] as List? ?? [];
  final targetDrops = target['drops'] as List? ?? [];
  for (var i = 0; i < sourceDrops.length || i < targetDrops.length; i++) {
    final from = i < targetDrops.length ? targetDrops[i] as Map : {};
    final to = i < sourceDrops.length ? sourceDrops[i] as Map : {};
    for (final key in [
      'catalog_key',
      'outcome',
      'min_level',
      'max_level',
      'chance_per_10000',
    ]) {
      if (from[key] != to[key]) {
        result.add(
          '掉落规则 ${i + 1} $key：${from[key] ?? "无"} → ${to[key] ?? "无"}',
        );
      }
    }
  }
  if ((source['growth_enabled'] == true) !=
      (target['growth_enabled'] == true)) {
    result.add(
      '自动升级：${target['growth_enabled'] == true} → ${source['growth_enabled'] == true}',
    );
  }
  for (var i = 0; i < 200; i++) {
    for (final k in rewardColumns.keys.skip(1)) {
      if (a[i][k] != b[i][k]) {
        result.add('${i + 1} 级 ${rewardColumns[k]}：${b[i][k]} → ${a[i][k]}');
      }
    }
  }
  return result;
}
