import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/reward_table.dart';

void main() {
  test('large experience does not relax currency bounds', () {
    final rows = rewardRows({});
    rows[149]['win_experience'] = 200000000;
    validateRewardRows(rows);
    rows[149]['win_gold'] = 1000001;
    expect(() => validateRewardRows(rows), throwsFormatException);
    rows[149]['win_gold'] = 0;
    rows[149]['win_experience'] = 2147483648;
    expect(() => validateRewardRows(rows), throwsFormatException);
  });
  test('drop-only changes are included in online preview', () {
    final a = {
      'drops': [
        {
          'catalog_key': 123,
          'outcome': 'win',
          'min_level': 1,
          'max_level': 150,
          'chance_per_10000': 100,
        },
      ],
    };
    final changes = rewardDiff(a, {});
    expect(changes.length, 5);
    expect(
      changes.any((s) => s.contains('chance_per_10000') && s.contains('100')),
      isTrue,
    );
  });
  test('CSV round-trip keeps 150 levels and zero values', () {
    final rows = rewardRows({'win_gold': 20});
    rows[149]['win_gold'] = 0;
    expect(rewardsFromCsv(rewardsToCsv(rows)), rows);
    expect(
      () => rewardsFromCsv(rewardsToCsv(rows).replaceFirst('150,0,', '149,0,')),
      throwsFormatException,
    );
    expect(() => validateRewardRows(rows, growth: true), throwsFormatException);
  });
  test('diff includes exact level, field and growth toggle', () {
    final a = {
      'levels': rewardRows({'win_gold': 20}),
      'growth_enabled': false,
    };
    final b = {
      'levels': rewardRows({'win_gold': 20}),
      'growth_enabled': true,
    };
    (b['levels'] as List<Map<String, int>>)[9]['loss_gold'] = 7;
    final diff = rewardDiff(a, b);
    expect(diff.length, 2);
    expect(diff.last, '10 级 失败金币：7 → 0');
  });
}
