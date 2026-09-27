import 'package:flutter_test/flutter_test.dart';
import 'package:kungfu_item_manager/config_inspect.dart';

void main() {
  test('level comparison detects missing levels and differing thresholds', () {
    final a = [
      {'level': 150, 'next_experience': 161771783},
      {'level': 151, 'next_experience': 166624937},
    ];
    expect(compareLevelCosts(a, a), isEmpty);
    final d = compareLevelCosts(a, [
      {'level': 150, 'next_experience': 30210},
      {'level': 200, 'next_experience': 0},
    ]);
    expect(d.length, 3);
    expect(d.first, contains('30210'));
    expect(d.last, contains('缺失'));
  });
}
