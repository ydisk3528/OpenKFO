import 'package:flutter/material.dart';

import 'reward_picker.dart';

class LevelGiftDialog extends StatefulWidget {
  const LevelGiftDialog({
    super.key,
    required this.gifts,
    this.api,
    this.tutorial = false,
  });
  final List<Map<String, dynamic>> gifts;
  final PickerApi? api;
  final bool tutorial;
  @override
  State<LevelGiftDialog> createState() => _LevelGiftDialogState();
}

class _LevelGiftDialogState extends State<LevelGiftDialog> {
  late final rows = widget.gifts
      .map(
        (e) =>
            Map<String, dynamic>.from(e)
              ..['items'] = List<int>.from(e['items'] as List? ?? []),
      )
      .toList();
  late final catalogue = widget.api == null
      ? null
      : RewardCatalog.load(widget.api!);
  String error = '';
  @override
  Widget build(BuildContext context) => AlertDialog(
    title: Text(widget.tutorial ? '新手引导完成奖励' : '升级礼包'),
    content: SizedBox(
      width: 700,
      height: 500,
      child: Column(
        children: [
          Text(
            widget.tutorial
                ? '武器为任选一件的候选（最多7件），玩家确认后领取；金币、点券及其他物品完成时自动发放，每个角色仅一次。'
                : '到达等级时自动全部发放；跨级逐级发放，不补发已有等级。每级最多8件物品，可另设金币、点券。',
          ),
          Expanded(
            child: ListView.builder(
              itemCount: rows.length,
              itemBuilder: (_, i) {
                final row = rows[i];
                Widget field(String key, String label) => SizedBox(
                  width: 150,
                  child: TextFormField(
                    initialValue: '${row[key] ?? 0}',
                    decoration: InputDecoration(labelText: label),
                    keyboardType: TextInputType.number,
                    onChanged: (v) => row[key] = int.tryParse(v) ?? -1,
                  ),
                );
                return Card(
                  key: ObjectKey(row),
                  child: Padding(
                    padding: const EdgeInsets.all(8),
                    child: Column(
                      children: [
                        Wrap(
                          spacing: 12,
                          children: [
                            if (!widget.tutorial) field('level', '到达等级'),
                            field('gold', '金币'),
                            field('tickets', '点券'),
                            if (!widget.tutorial)
                              IconButton(
                                onPressed: () =>
                                    setState(() => rows.removeAt(i)),
                                icon: const Icon(Icons.delete_outline),
                              ),
                          ],
                        ),
                        if (catalogue != null)
                          RewardItemsField(
                            catalog: catalogue!,
                            items: List<int>.from(row['items'] as List),
                            onChanged: (v) => setState(() => row['items'] = v),
                          ),
                      ],
                    ),
                  ),
                );
              },
            ),
          ),
          Text(error, style: const TextStyle(color: Colors.red)),
          if (!widget.tutorial)
            TextButton(
              onPressed: rows.length >= 199
                  ? null
                  : () => setState(
                      () => rows.add({
                        'level': 2,
                        'items': <int>[],
                        'gold': 0,
                        'tickets': 0,
                      }),
                    ),
              child: const Text('新增等级礼包'),
            ),
        ],
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      ),
      FilledButton(
        onPressed: () {
          try {
            final seen = <int>{};
            var total = 0;
            for (final row in rows) {
              final level = row['level'] as int? ?? 0;
              final gold = row['gold'] as int? ?? 0,
                  tickets = row['tickets'] as int? ?? 0;
              final items = row['items'] as List;
              if (!widget.tutorial &&
                  (level < 2 || level > 200 || !seen.add(level)))
                throw const FormatException('等级须为不重复的2–200');
              if (gold < 0 ||
                  gold > 1000000 ||
                  tickets < 0 ||
                  tickets > 1000000 ||
                  items.length > 8)
                throw const FormatException('金币、点券0–1000000，每份最多8件物品');
              if (!widget.tutorial &&
                  gold == 0 &&
                  tickets == 0 &&
                  items.isEmpty)
                throw const FormatException('礼包不能为空');
              total += items.length;
            }
            if (total > 150) throw const FormatException('合计不能超过150件物品');
            Navigator.pop(context, rows);
          } catch (e) {
            setState(() => error = '$e');
          }
        },
        child: const Text('应用到草稿'),
      ),
    ],
  );
}
