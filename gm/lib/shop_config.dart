import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart' show ScrollCacheExtent;

class ShopConfigPage extends StatefulWidget {
  const ShopConfigPage({
    super.key,
    required this.api,
    this.environment = '当前环境',
  });
  final String environment;
  final Future<dynamic> Function(Map<String, dynamic>) api;
  @override
  State<ShopConfigPage> createState() => _ShopConfigPageState();
}

class _ShopConfigPageState extends State<ShopConfigPage> {
  Map<String, dynamic>? data, item;
  String query = '', message = '', currency = 'ticket';
  bool busy = true,
      enabled = false,
      dirty = false,
      serverExpiry = false,
      recommended = false;
  final price = TextEditingController(text: '100');
  final recommendationPriority = TextEditingController(text: '0');
  final days = TextEditingController(text: '365');
  final quantity = TextEditingController(text: '1');
  final form = GlobalKey<FormState>();
  final selected = <String>{};
  String? pendingSignature, pendingId;
  String batchCurrency = 'ticket', batchAmount = '100';
  String category = '全部商品';
  final scroll = ScrollController();
  Timer? imageTimer;
  int columns = 1;
  double viewportHeight = 0;
  bool loadingImages = false;
  final images = <String, Uint8List>{};
  final requestedImages = <String>{};
  final imageStatus = <String, bool>{};
  bool scanningImages = false;
  List<dynamic> get filteredItems => (data?['items'] as List? ?? []).where((i) {
    final kind = i['kind'];
    final aliases = kind == 20
        ? '头饰 头部饰品'
        : kind == 21
        ? '背饰 背部饰品 翅膀'
        : '';
    if (!'${i['id']} ${i['name']} ${i['category']} $aliases'.contains(
      query.trim(),
    ))
      return false;
    final text = '${i['category']} ${i['group']}';
    return switch (category) {
      '缺少图片' => imageStatus[i['key']] == false,
      '推荐/优惠' => data?['offers']?[i['key']]?['recommended'] == true,
      '武器' => kind == 25 || kind == 26,
      '头饰' => kind == 20,
      '背饰/翅膀' => kind == 21,
      '闯关门票' => i['category'] == '闯关门票',
      '宠物/法宝' =>
        text.contains('宠物') || text.contains('法宝') || text.contains('护符'),
      '造型换装' =>
        (kind is int && kind >= 12 && kind <= 21) ||
            [31, 77, 79, 83].contains(kind),
      '材料/药水' => [50, 60, 61, 64, 68].contains(kind),
      '功能道具' =>
        text.contains('功能') || text.contains('礼包') || text.contains('婚礼'),
      _ => true,
    };
  }).toList();

  Future<void> scanImages() async {
    if (busy || scanningImages || data == null) return;
    final keys = (data!['items'] as List)
        .map((i) => i['key'] as String)
        .toList();
    setState(() {
      scanningImages = true;
      imageStatus.clear();
    });
    try {
      for (var start = 0; start < keys.length; start += 24) {
        final batchKeys = keys.skip(start).take(24).toList();
        final result = await widget.api({
          'operation': 'shop_image_status',
          'keys': batchKeys,
        });
        if (!mounted) return;
        if (result is! Map || batchKeys.any((k) => result[k] is! bool)) {
          throw const FormatException('图片检测结果不完整，未将未确认商品判为缺图');
        }
        setState(() {
          for (final key in batchKeys) {
            imageStatus[key] = result[key] as bool;
          }
          message =
              '图片检测 ${imageStatus.length}/${keys.length}，无可用图片 ${imageStatus.values.where((v) => !v).length} 件';
        });
      }
    } catch (e) {
      if (mounted) setState(() => message = '检测未完成：$e。未检测商品不会归入缺图。');
    } finally {
      if (mounted) setState(() => scanningImages = false);
    }
  }

  Future<void> loadImages() async {
    if (!mounted || loadingImages || !scroll.hasClients) return;
    final firstRow = (scroll.offset.clamp(0, double.infinity) / 166).floor();
    final keys = filteredItems
        .skip(firstRow * columns)
        .take(((viewportHeight / 166).ceil() + 1) * columns)
        .take(24)
        .where(
          (i) =>
              (i['fields'] as List? ?? []).length > 9 &&
              requestedImages.add(i['key'] as String),
        )
        .map((i) => i['key'] as String)
        .toList();
    if (keys.isEmpty) return;
    loadingImages = true;
    try {
      final result = await widget.api({
        'operation': 'shop_images',
        'keys': keys,
      });
      if (!mounted) return;
      setState(() {
        for (final key in keys) {
          if (result[key] is String) images[key] = base64Decode(result[key]);
        }
        while (images.length > 256) {
          final oldest = images.keys.firstWhere((key) => key != item?['key']);
          images.remove(oldest);
          requestedImages.remove(oldest);
        }
      });
    } catch (_) {
      // Retry unavailable requests on explicit refresh, not on every scroll.
    } finally {
      loadingImages = false;
      if (mounted) scheduleImages();
    }
  }

  void scheduleImages() {
    imageTimer?.cancel();
    imageTimer = Timer(const Duration(milliseconds: 120), loadImages);
  }

  void changeFilter(void Function() change) {
    setState(() {
      change();
    });
    if (scroll.hasClients) scroll.jumpTo(0);
    scheduleImages();
  }

  Widget productImage(String key, double size) => SizedBox(
    width: size,
    height: size,
    child: images[key] == null
        ? const Icon(Icons.inventory_2_outlined, size: 36)
        : Image.memory(
            images[key]!,
            fit: BoxFit.contain,
            errorBuilder: (_, e, s) => const Icon(Icons.broken_image_outlined),
          ),
  );

  String operationId(Map<String, dynamic> request) {
    final signature = jsonEncode(request);
    if (signature != pendingSignature) {
      pendingSignature = signature;
      pendingId = 'shop-${DateTime.now().microsecondsSinceEpoch}';
    }
    return pendingId!;
  }

  @override
  void initState() {
    super.initState();
    scroll.addListener(scheduleImages);
    load();
  }

  @override
  void dispose() {
    imageTimer?.cancel();
    scroll.dispose();
    price.dispose();
    recommendationPriority.dispose();
    days.dispose();
    quantity.dispose();
    super.dispose();
  }

  void select(Map<String, dynamic> next) {
    item = next;
    final config = data!['offers'][item!['key']] as Map?;
    currency = config?['currency'] ?? 'ticket';
    enabled = config?['enabled'] ?? false;
    recommended = config?['recommended'] ?? false;
    recommendationPriority.text = '${config?['recommendation_priority'] ?? 0}';
    price.text = '${config?['price'] ?? 100}';
    serverExpiry = config == null || (config['server_expiry_days'] ?? 0) > 0;
    days.text =
        '${serverExpiry ? (config?['server_expiry_days'] ?? 30) : config?['days'] ?? 365}';
    quantity.text = '${config?['quantity'] ?? 1}';
    dirty = false;
  }

  Future<void> load() async {
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({'operation': 'shop_catalog'}),
      );
      if (!mounted) return;
      setState(() {
        data = result;
        imageStatus.clear();
        requestedImages.removeWhere((key) => !images.containsKey(key));
        busy = false;
        if (item != null) select(item!);
      });
      scheduleImages();
    } catch (e) {
      if (mounted) {
        setState(() {
          message = '$e';
          busy = false;
        });
      }
    }
  }

  Future<bool> discard() async {
    if (!dirty) return true;
    return await showDialog<bool>(
          context: context,
          builder: (context) => AlertDialog(
            title: const Text('有未保存的商城配置'),
            content: const Text('是否丢弃当前修改？'),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(context, false),
                child: const Text('继续编辑'),
              ),
              FilledButton(
                onPressed: () => Navigator.pop(context, true),
                child: const Text('丢弃修改'),
              ),
            ],
          ),
        ) ==
        true;
  }

  Future<void> saveRecommendationOrder({bool pin = false}) async {
    if (busy || dirty || item == null) return;
    final priority = int.tryParse(recommendationPriority.text);
    if (!pin && (priority == null || priority < 0 || priority > 1000000)) {
      setState(() => message = '推荐排序请填写 0–1000000 的整数');
      return;
    }
    setState(() { busy = true; message = '正在保存推荐排序…'; });
    try {
      final request = <String, dynamic>{
        'operation': 'shop_rank', 'keys': [item!['key']],
        'recommendation_priority': pin ? 0 : priority,
        'pin_recommended': pin,
      };
      final result = await widget.api({...request, 'id': operationId(request)});
      if (result['recommendation_priority_saved'] != true) {
        throw StateError('管理接口未确认推荐排序，请刷新核对并更新管理接口。');
      }
      if (!mounted) return;
      setState(() {
        final offer = data!['offers'][item!['key']] as Map;
        offer['recommendation_priority'] = result['recommendation_priority'];
        recommendationPriority.text = '${result['recommendation_priority']}';
        if (pin) { offer['recommended'] = true; recommended = true; }
        busy = false; pendingId = pendingSignature = null;
        message = '${widget.environment} · ${result['message']}';
      });
    } catch (e) {
      if (mounted) setState(() { busy = false; message = '排序保存未确认：$e'; });
    }
  }

  Future<void> save() async {
    if (busy || !form.currentState!.validate()) return;
    setState(() {
      busy = true;
      message = "正在保存到${widget.environment}，请稍候…";
    });
    try {
      final request = <String, dynamic>{
        'operation': 'shop_save',
        'key': item!['key'],
        'currency': currency,
        'price': int.parse(price.text),
        'days': int.parse(days.text),
        if (item!['stackable'] != true)
          'server_expiry_days': serverExpiry ? int.parse(days.text) : 0,
        'quantity': int.parse(quantity.text),
        'enabled': enabled,
        'recommended': recommended,
      };
      final result = await widget.api({...request, 'id': operationId(request)});
      if (result['recommendation_saved'] != true) {
        throw StateError('服务器尚未确认推荐设置，请更新管理接口后刷新核对。');
      }
      if (request.containsKey('server_expiry_days') &&
          result['expiry_policy_saved'] != true) {
        throw StateError('服务端未确认期限策略，请更新对应环境的管理接口。其他商品设置可能已保存，请刷新核对。');
      }
      if (!mounted) return;
      setState(() {
        final offers = data!['offers'] as Map;
        offers[request['key']] = {
          'recommendation_priority': offers[request['key']]?['recommendation_priority'],
          for (final key in [
            'currency',
            'price',
            'days',
            'server_expiry_days',
            'quantity',
            'enabled',
            'recommended',
          ])
            key: request[key],
        };
        message = '${widget.environment} · ${result['message']}';
        dirty = false;
        busy = false;
        pendingId = pendingSignature = null;
      });
    } catch (e) {
      if (mounted) {
        setState(() {
          message = '$e';
          busy = false;
        });
      }
    }
  }

  Future<void> batch(
    bool publish, {
    bool all = false,
    List<String>? explicitKeys,
  }) async {
    if (busy ||
        scanningImages ||
        data == null ||
        (!all && (explicitKeys ?? selected.toList()).isEmpty)) {
      return;
    }
    if (!await discard() || !mounted) return;
    final keys = List<String>.of(explicitKeys ?? selected.toList())..sort();
    final count = all ? (data!['items'] as List).length : keys.length;
    final accepted = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('${all ? '全部' : '批量'}${publish ? '上架' : '下架'}当前环境商品'),
        content: Text(
          publish
              ? '将上架 $count 件可售商品。已有价格、币种、期限和数量保持不变。\n\n未配置商品默认：100 点券；装备 365 天；消耗品每次 1 个。'
              : all
              ? '将下架当前环境商城全部销售记录，包括当前搜索结果之外的商品。价格和发货设置保留。'
              : '将下架选中的 $count 件商品，保留价格和发货设置。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('确认执行'),
          ),
        ],
      ),
    );
    if (accepted != true || !mounted) return;
    final request = <String, dynamic>{
      'operation': 'shop_batch',
      'enabled': publish,
      'all': all,
      'keys': all ? <String>[] : keys,
    };
    setState(() => busy = true);
    try {
      final result = await widget.api({...request, 'id': operationId(request)});
      if (!mounted) return;
      setState(() {
        message = result['message'];
        dirty = false;
        pendingId = pendingSignature = null;
      });
      await load();
    } catch (error) {
      if (mounted) {
        setState(() {
          busy = false;
          message = '$error';
        });
      }
    }
  }

  Future<void> batchPrices() async {
    if (busy || data == null || selected.isEmpty) return;
    if (!await discard() || !mounted) return;
    final keys = selected.toList()..sort();
    final amount = TextEditingController(text: batchAmount);
    var chosen = batchCurrency;
    String? error;
    final accepted = await showDialog<bool>(
      context: context,
      builder: (c) => StatefulBuilder(
        builder: (c, update) => AlertDialog(
          title: Text('批量改价 · ${widget.environment} · ${keys.length} 件'),
          content: SizedBox(
            width: 520,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const Text(
                  '仅修改所选商品已有销售记录的币种和售价。多条销售规格统一改价；期限、数量、上下架及其他标记保留。未配置商品跳过，不自动上架。',
                ),
                const SizedBox(height: 16),
                DropdownButtonFormField<String>(
                  initialValue: chosen,
                  decoration: const InputDecoration(labelText: '批量币种'),
                  items: const [
                    DropdownMenuItem(value: 'ticket', child: Text('点券')),
                    DropdownMenuItem(value: 'gold', child: Text('金币')),
                  ],
                  onChanged: (v) => update(() => chosen = v!),
                ),
                const SizedBox(height: 16),
                TextField(
                  key: const ValueKey('batch-price'),
                  controller: amount,
                  keyboardType: TextInputType.number,
                  decoration: InputDecoration(
                    labelText: '统一售价',
                    errorText: error,
                  ),
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(c, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () {
                final value = int.tryParse(amount.text.trim());
                if (value == null || value < 1 || value > 2147483647) {
                  update(() => error = '请输入 1–2147483647 的整数');
                  return;
                }
                Navigator.pop(c, true);
              },
              child: const Text('确认改价'),
            ),
          ],
        ),
      ),
    );
    if (accepted == true && mounted) {
      batchCurrency = chosen;
      batchAmount = amount.text.trim();
      final request = <String, dynamic>{
        'operation': 'shop_prices',
        'keys': keys,
        'currency': chosen,
        'price': int.parse(batchAmount),
      };
      setState(() {
        busy = true;
        message = '正在批量改价，请稍候…';
      });
      try {
        final result = await widget.api({
          ...request,
          'id': operationId(request),
        });
        if (mounted) {
          setState(() {
            final offers = data!['offers'] as Map;
            for (final key in keys) {
              if (offers[key] != null) {
                offers[key]['currency'] = request['currency'];
                offers[key]['price'] = request['price'];
              }
            }
            if (item != null) select(item!);
            dirty = false;
            pendingId = pendingSignature = null;
            message = result['message'];
          });
        }
      } catch (e) {
        if (mounted) setState(() => message = '$e');
      } finally {
        if (mounted) setState(() => busy = false);
      }
    }
    await Future<void>.delayed(const Duration(milliseconds: 300));
    amount.dispose();
  }

  Widget number(TextEditingController controller, String label, int max) =>
      TextFormField(
        controller: controller,
        enabled: !busy,
        keyboardType: TextInputType.number,
        decoration: InputDecoration(labelText: label),
        onChanged: (_) => setState(() => dirty = true),
        validator: (text) {
          final v = int.tryParse(text ?? '');
          return v == null || v < 1 || v > max ? '请输入 1–$max' : null;
        },
      );

  @override
  Widget build(BuildContext context) {
    final items = filteredItems;
    return PopScope(
      canPop: !dirty && !busy,
      onPopInvokedWithResult: (didPop, result) async {
        if (didPop || busy) return;
        if (await discard() && mounted) {
          setState(() => dirty = false);
          WidgetsBinding.instance.addPostFrameCallback((_) {
            if (mounted) Navigator.pop(context);
          });
        }
      },
      child: Scaffold(
        appBar: AppBar(title: Text('${widget.environment} · 商城配置')),
        body: Column(
          children: [
            if (busy) const LinearProgressIndicator(),
            SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              child: Padding(
                padding: const EdgeInsets.all(12),
                child: Row(
                  children: [
                    for (final name in [
                      '全部商品',
                      '缺少图片',
                      '推荐/优惠',
                      '武器',
                      '头饰',
                      '背饰/翅膀',
                      '闯关门票',
                      '宠物/法宝',
                      '造型换装',
                      '材料/药水',
                      '功能道具',
                    ])
                      Padding(
                        padding: const EdgeInsets.only(right: 8),
                        child: ChoiceChip(
                          label: Text(name),
                          selected: category == name,
                          onSelected: busy
                              ? null
                              : (_) => changeFilter(() => category = name),
                        ),
                      ),
                  ],
                ),
              ),
            ),
            if (category == '推荐/优惠')
              const Padding(
                padding: EdgeInsets.symmetric(horizontal: 16),
                child: Text('推荐商品显示在游戏推荐页。在武器分类中选择商品，打开“加入推荐”并保存；价格独立配置。'),
              ),

            Padding(
              padding: const EdgeInsets.all(12),
              child: Wrap(
                spacing: 10,
                runSpacing: 8,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  OutlinedButton(
                    onPressed: busy || scanningImages ? null : scanImages,
                    child: Text(scanningImages ? '检测图片中…' : '检测全部商品图片'),
                  ),
                  if (category == '缺少图片')
                    OutlinedButton(
                      onPressed: busy || scanningImages || filteredItems.isEmpty
                          ? null
                          : () => batch(
                              false,
                              explicitKeys: filteredItems
                                  .map((i) => i['key'] as String)
                                  .toList(),
                            ),
                      child: const Text('批量下架当前缺图商品'),
                    ),
                  OutlinedButton(
                    onPressed: busy || data == null
                        ? null
                        : () => batch(true, all: true),
                    child: const Text('全部上架'),
                  ),
                  OutlinedButton(
                    onPressed: busy || data == null
                        ? null
                        : () => batch(false, all: true),
                    child: const Text('全部下架'),
                  ),
                  FilledButton(
                    onPressed: busy || selected.isEmpty
                        ? null
                        : () => batch(true),
                    child: const Text('选中上架'),
                  ),
                  OutlinedButton(
                    onPressed: busy || selected.isEmpty
                        ? null
                        : () => batch(false),
                    child: const Text('选中下架'),
                  ),
                  FilledButton(
                    onPressed: busy || selected.isEmpty ? null : batchPrices,
                    child: const Text('批量改价'),
                  ),
                  Text('已选 ${selected.length} 件'),
                  IconButton(
                    onPressed: busy ? null : load,
                    tooltip: '刷新当前环境商品',
                    icon: const Icon(Icons.refresh),
                  ),
                ],
              ),
            ),
            Expanded(
              child: Row(
                children: [
                  Expanded(
                    flex: 3,
                    child: Column(
                      children: [
                        Padding(
                          padding: const EdgeInsets.all(12),
                          child: TextField(
                            decoration: const InputDecoration(
                              labelText: '搜索商品',
                            ),
                            onChanged: (v) => changeFilter(() => query = v),
                          ),
                        ),
                        Wrap(
                          spacing: 8,
                          children: [
                            TextButton(
                              onPressed: busy
                                  ? null
                                  : () => setState(
                                      () => selected.addAll(
                                        items.map(
                                          (row) => row['key'] as String,
                                        ),
                                      ),
                                    ),
                              child: const Text('选择搜索结果'),
                            ),
                            TextButton(
                              onPressed: busy
                                  ? null
                                  : () => setState(selected.clear),
                              child: const Text('清空选择'),
                            ),
                          ],
                        ),
                        Expanded(
                          child: LayoutBuilder(
                            builder: (context, constraints) {
                              final nextColumns =
                                  ((constraints.maxWidth - 24) / 370)
                                      .ceil()
                                      .clamp(1, 100);
                              if (columns != nextColumns ||
                                  viewportHeight != constraints.maxHeight) {
                                columns = nextColumns;
                                viewportHeight = constraints.maxHeight;
                                scheduleImages();
                              }
                              return Scrollbar(
                                controller: scroll,
                                thumbVisibility: true,
                                child: GridView.builder(
                                  controller: scroll,
                                  scrollCacheExtent:
                                      const ScrollCacheExtent.pixels(166),
                                  addAutomaticKeepAlives: false,
                                  padding: const EdgeInsets.all(12),
                                  gridDelegate:
                                      const SliverGridDelegateWithMaxCrossAxisExtent(
                                        maxCrossAxisExtent: 360,
                                        mainAxisExtent: 156,
                                        crossAxisSpacing: 10,
                                        mainAxisSpacing: 10,
                                      ),
                                  itemCount: items.length,
                                  itemBuilder: (context, index) {
                                    final row = items[index];
                                    final offer = data!['offers'][row['key']];
                                    return Card(
                                      key: ValueKey(row['key']),
                                      clipBehavior: Clip.antiAlias,
                                      color: item?['key'] == row['key']
                                          ? Theme.of(context)
                                                .colorScheme
                                                .secondaryContainer
                                          : null,
                                      child: ListTile(
                                        leading: Checkbox(
                                          value: selected.contains(row['key']),
                                          onChanged: busy
                                              ? null
                                              : (checked) => setState(() {
                                                  if (checked == true) {
                                                    selected.add(row['key']);
                                                  } else {
                                                    selected.remove(row['key']);
                                                  }
                                                }),
                                        ),
                                        selected: item?['key'] == row['key'],
                                        title: Text(row['name']),
                                        subtitle: Column(
                                          crossAxisAlignment:
                                              CrossAxisAlignment.start,
                                          children: [
                                            Text(
                                              '${row['id']} · ${offer?['enabled'] == true ? '已上架' : '未上架'}',
                                            ),
                                            Row(
                                              children: [
                                                productImage(row['key'], 64),
                                                const SizedBox(width: 8),
                                                Flexible(
                                                  child: Text(
                                                    offer == null
                                                        ? '未配置售价'
                                                        : '${offer['price']} ${offer['currency'] == 'gold' ? '金币' : '点券'}',
                                                  ),
                                                ),
                                              ],
                                            ),
                                          ],
                                        ),
                                        onTap: busy
                                            ? null
                                            : () async {
                                                if (await discard() &&
                                                    mounted) {
                                                  setState(
                                                    () => select(
                                                      Map<String, dynamic>.from(
                                                        row,
                                                      ),
                                                    ),
                                                  );
                                                }
                                              },
                                      ),
                                    );
                                  },
                                ),
                              );
                            },
                          ),
                        ),
                        Padding(
                          padding: const EdgeInsets.all(8),
                          child: Text('共 ${items.length} 件 · 向下滚动浏览'),
                        ),
                      ],
                    ),
                  ),
                  const VerticalDivider(width: 1),
                  Expanded(
                    flex: 2,
                    child: item == null
                        ? const Center(child: Text('选择商品，配置价格和发货内容'))
                        : SingleChildScrollView(
                            padding: const EdgeInsets.all(24),
                            child: Form(
                              key: form,
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.stretch,
                                children: [
                                  Center(
                                    child: productImage(item!['key'], 100),
                                  ),
                                  Text(
                                    item!['name'],
                                    style: Theme.of(context)
                                        .textTheme
                                        .headlineSmall,
                                  ),
                                  SwitchListTile(
                                    title: const Text('上架销售'),
                                    value: enabled,
                                    onChanged: busy
                                        ? null
                                        : (v) => setState(() {
                                            enabled = v;
                                            dirty = true;
                                          }),
                                  ),
                                  SwitchListTile(
                                    title: const Text('加入推荐（武器、背饰）'),
                                    subtitle: const Text('推荐页复用同一商品、价格和购买校验'),
                                    value: recommended,
                                    onChanged: busy || ![25, 21].contains(item!['kind'])
                                        ? null
                                        : (v) => setState(() {
                                            recommended = v;
                                            dirty = true;
                                          }),
                                  ),
                                  if ([25, 21].contains(item!['kind'])) ...[
                                    TextField(
                                      controller: recommendationPriority,
                                      keyboardType: TextInputType.number,
                                      enabled: !busy && data!['offers'][item!['key']]?['recommendation_priority'] != null,
                                      decoration: const InputDecoration(labelText: '推荐排序', helperText: '数字越大越靠前；0 为默认顺序'),
                                    ),
                                    const SizedBox(height: 8),
                                    Wrap(spacing: 8, runSpacing: 8, children: [
                                      OutlinedButton(onPressed: busy || dirty || data!['offers'][item!['key']]?['recommendation_priority'] == null ? null : () => saveRecommendationOrder(), child: const Text('保存排序')),
                                      OutlinedButton.icon(onPressed: busy || dirty || data!['offers'][item!['key']]?['recommendation_priority'] == null ? null : () => saveRecommendationOrder(pin: true), icon: const Icon(Icons.vertical_align_top), label: const Text('置顶推荐')),
                                    ]),
                                    if (data!['offers'][item!['key']]?['recommendation_priority'] == null)
                                      const Text('当前管理接口尚未支持推荐排序，待更新后可设置。'),
                                    if (dirty) const Text('请先保存当前商品修改，再调整排序。'),
                                    const SizedBox(height: 12),
                                  ],
                                  SegmentedButton<String>(
                                    segments: const [
                                      ButtonSegment(
                                        value: 'ticket',
                                        label: Text('点券'),
                                      ),
                                      ButtonSegment(
                                        value: 'gold',
                                        label: Text('金币'),
                                      ),
                                    ],
                                    selected: {currency},
                                    onSelectionChanged: busy
                                        ? null
                                        : (v) => setState(() {
                                            currency = v.first;
                                            dirty = true;
                                          }),
                                  ),
                                  const SizedBox(height: 20),
                                  number(price, '售价', 2147483647),
                                  if (item!['kind'] == 30)
                                    const Text(
                                      '宠物/法宝每次购买 1 件，初始耐久 100；使用消耗在宠物/法宝配置中设置。',
                                    ),
                                  if (item!['supported'] == false)
                                    const Text('该类型尚未支持上架，可查看图片并批量下架已有商品。'),
                                  const SizedBox(height: 20),
                                  if (item!['stackable'] == true)
                                    number(quantity, '每次购买数量', 999)
                                  else ...[
                                    DropdownButtonFormField<bool>(
                                      value: serverExpiry,
                                      decoration: const InputDecoration(
                                        labelText: '购买后有效期',
                                      ),
                                      items: const [
                                        DropdownMenuItem(
                                          value: true,
                                          child: Text('限时装备'),
                                        ),
                                        DropdownMenuItem(
                                          value: false,
                                          child: Text('永久装备（365+）'),
                                        ),
                                      ],
                                      onChanged: busy
                                          ? null
                                          : (v) => setState(() {
                                              serverExpiry = v ?? true;
                                              dirty = true;
                                            }),
                                    ),
                                    if (serverExpiry) ...[
                                      const SizedBox(height: 12),
                                      number(days, '有效天数', 3650),
                                      Wrap(
                                        spacing: 8,
                                        children: [
                                          for (final n in [1, 7, 30, 365])
                                            ActionChip(
                                              label: Text('$n天'),
                                              onPressed: busy
                                                  ? null
                                                  : () => setState(() {
                                                      days.text = '$n';
                                                      dirty = true;
                                                    }),
                                            ),
                                        ],
                                      ),
                                    ],
                                    const Text('从购买时开始计时；只影响之后购买的物品，已有背包不变。'),
                                    if (item!['kind'] == 20 ||
                                        item!['kind'] == 21)
                                      const Text('头饰、背饰通过“装备／卸下”操作，不作为消耗品使用。'),
                                  ],
                                  const SizedBox(height: 24),
                                  FilledButton(
                                    onPressed:
                                        busy || item!['supported'] == false
                                        ? null
                                        : save,
                                    child: const Text('保存商城配置'),
                                  ),
                                  const SizedBox(height: 16),
                                  const Text(
                                    '直接保存到当前环境的数据库；下架保留价格与发货设置。游戏会缓存商品，修改后请重新登录。',
                                  ),
                                ],
                              ),
                            ),
                          ),
                  ),
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.all(16),
              child: SelectableText(message),
            ),
          ],
        ),
      ),
    );
  }
}
