import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

class WeaponConfigPage extends StatefulWidget {
  const WeaponConfigPage({super.key, required this.api});
  final Future<dynamic> Function(Map<String, dynamic>) api;
  @override
  State<WeaponConfigPage> createState() => _WeaponConfigPageState();
}

/// 发版包可勾选的内容分组，和后端 weapon_package.go 的 packageSections 一致。
/// 顺序即界面上的勾选顺序。
const packageSections = <String, String>{
  'config': '配置包（config.spf2）',
  'model': '模型 .dff',
  'texture': '贴图 .png',
  'icon': '图标',
  'animation': '动作 .anm',
  'audio': '音效 .wav',
  'effect': '特效',
};

class _WeaponConfigPageState extends State<WeaponConfigPage> {
  Map<String, dynamic>? data, weapon;
  Map<String, dynamic> _clientInfo = {};
  final form = GlobalKey<FormState>();
  List<Map<String, dynamic>> rules = [];
  String query = '', message = '';
  String weaponType = '全部类型';
  bool busy = true, dirty = false, failed = false;
  String? selectedAction;
  int editorVersion = 0;
  /// 最近一次导出发版包的结果（后端 packageResult）；空 map 表示还没导出过。
  Map<String, dynamic> exportResult = {};
  /// 最近一次导出是不是合并包（只含当前武器的配置）。
  bool lastExportMerge = false;
  /// 最近一次「导入武器包」的结果（后端 mergeImportReport）；空 map 表示还没导入过。
  Map<String, dynamic> mergeImportResult = {};

  @override
  void initState() {
    super.initState();
    load();
  }

  Future<void> load({int? prefer}) async {
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({'operation': 'weapon_catalog'}),
      );
      Map<String, dynamic> client = {};
      try {
        client = Map<String, dynamic>.from(
          await widget.api({'operation': 'client_directory_get'}),
        );
      } catch (_) {
        // The card falls back to whatever was shown before.
      }
      if (!mounted) return;
      setState(() {
        data = result;
        if (client.isNotEmpty) _clientInfo = client;
        final weapons = (data!['weapons'] as List);
        final selected = weapons.where(
          (w) => w['id'] == (prefer ?? weapon?['id'] ?? 253013),
        );
        if (selected.isNotEmpty) {
          select(Map<String, dynamic>.from(selected.first));
        } else if (weapons.isNotEmpty) {
          select(Map<String, dynamic>.from(weapons.first));
        }
        busy = false;
      });
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  /// Self-made weapons registered in settings.json, keyed by weapon id.
  /// They exist only as rows injected into item.txt / itemact.txt, so the
  /// client binary stays untouched.
  Map<String, dynamic> get created =>
      Map<String, dynamic>.from(data?['created'] as Map? ?? const {});

  bool isCreated(dynamic id) => created.containsKey('$id');

  /// 编辑集里有、当前客户端自己的 config.spf2 里还没有的武器编号。
  /// 武器列表是「客户端 + 全局编辑集」渲染出来的，编辑集不随客户端切换，
  /// 所以自建武器会在每个客户端上都显示；这些编号就是"尚未部署到本客户端"的。
  Set<String> get undeployedIDs => {
    for (final id in (data?['undeployed'] as List? ?? [])) '$id',
  };

  bool isDeployed(dynamic id) => !undeployedIDs.contains('$id');

  /// 「共 N 件武器 · 本客户端实有 X · 编辑集额外 Y」。编辑集全局共享、换客户端
  /// 不会清掉自建武器，所以要把"客户端真有的"和"只是编辑集里的"分开报清楚。
  String deploySummary(List allWeapons) {
    final extra = allWeapons.where((w) => !isDeployed(w['id'])).length;
    if (extra == 0) return '共 ${allWeapons.length} 件武器';
    return '共 ${allWeapons.length} 件武器 · 本客户端实有 '
        '${allWeapons.length - extra} · 编辑集额外 $extra（未部署）';
  }

  /// The registered blueprint behind a self-made weapon, empty for shipped ones.
  Map<String, dynamic> blueprintOf(dynamic id) {
    final value = created['$id'];
    return value is Map ? Map<String, dynamic>.from(value) : const {};
  }

  /// 自建武器的来源说明：有供体就写供体，从零创建的说明动作需要逐个定义。
  String donorSummary(dynamic id) {
    final donor = blueprintOf(id)['donor'];
    if (donor == null || donor == 0) {
      return '从零创建（无供体）· 动作与命中属性逐个状态定义';
    }
    return '供体 $donor · 连招随供体自动补齐';
  }

  /// Combo completions recorded in settings.json: weapon id -> reference weapon.
  Map<String, dynamic> get comboFixes =>
      Map<String, dynamic>.from(data?['combos'] as Map? ?? const {});

  int? comboDonorOf(dynamic id) {
    final value = comboFixes['$id'];
    if (value == null) return null;
    return value is int ? value : int.tryParse('$value');
  }

  String weaponName(int id) {
    for (final w in (data?['weapons'] as List? ?? [])) {
      if (w['id'] == id) return '${w['name']}';
    }
    return '$id';
  }

  /// The client this editor reads and writes, plus the server config files that
  /// validate its config.spf2. Loaded separately so switching clients is
  /// reflected without reloading the whole catalogue.
  Map<String, dynamic> get clientInfo => _clientInfo;

  /// config.spf2 digest as the game server sees it — the value to put into the
  /// server's config.json `config_hash`.
  String get clientConfigHash => '${_clientInfo['config_hash'] ?? ''}';

  Future<void> reloadClient() async {
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({'operation': 'client_directory_get'}),
      );
      if (mounted) setState(() => _clientInfo = result);
    } catch (_) {
      // The card simply keeps the previous picture.
    }
  }

  Future<void> chooseClient(/* optional initial pick */) async {
    final picked = await showDialog<String>(
      context: context,
      builder: (_) => _ClientPickerDialog(
        current: '${_clientInfo['directory'] ?? ''}',
        detected: [
          for (final value in (_clientInfo['detected'] as List? ?? []))
            Map<String, dynamic>.from(value as Map),
        ],
      ),
    );
    if (picked == null || picked.isEmpty || !mounted) return;
    final prefer = weapon?['id'] as int?;
    setState(() {
      busy = true;
      failed = false;
      message = '正在切换客户端…';
    });
    try {
      final result = await widget.api({
        'operation': 'client_directory_set',
        'directory': picked,
      });
      if (!mounted) return;
      setState(() {
        busy = false;
        _clientInfo = Map<String, dynamic>.from(result is Map ? result : {});
        message = '${(result as Map)['message'] ?? '已切换客户端'}';
      });
      await load(prefer: prefer);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

    Future<void> rebaseClient() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('按当前客户端配置重新采集基线'),
        content: const Text(
          '把客户端现在的 config.spf2 记为新的基线，之后的编辑都以它为准。\n\n'
          '只在客户端被别的程序改过（例如它自己的更新器）而我们手上还是旧基线时才需要这样做。'
          '旧基线会另存一份备份。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('重新采集'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    final prefer = weapon?['id'] as int?;
    setState(() {
      busy = true;
      failed = false;
      message = '正在重新采集基线…';
    });
    try {
      final result = await widget.api({'operation': 'weapon_client_rebase'});
      if (!mounted) return;
      setState(() {
        busy = false;
        message = '${(result as Map)['message'] ?? '已重新采集基线'}';
      });
      await load(prefer: prefer);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  /// The dialog works with the *value* of item.txt column 2, while the
  /// catalogue exposes the human label. Map back through the same table.
  String typeValueFor(String label) {
    for (final type in (data?['types'] as List? ?? [])) {
      if ('${type['label']}' == label) return '${type['value']}';
    }
    return '';
  }

  /// Weapons that can act as a donor: everything the client already ships.
  List<Map<String, dynamic>> get donors => [
    for (final w in (data?['weapons'] as List? ?? []))
      if (!isCreated(w['id'])) Map<String, dynamic>.from(w as Map),
  ];

  /// Every id already claimed by item.txt, including ids that carry no action
  /// row. Authoritative set for the reserved-range picker.
  List<int> get usedIDs {
    final ids = data?['used_ids'] as List?;
    if (ids != null) return [for (final id in ids) id as int];
    return [for (final w in (data?['weapons'] as List? ?? [])) w['id'] as int];
  }

  /// Lowest unused id in the reserved self-made range.
  int suggestID() {
    final low = data?['blueprint_min'] as int? ?? 253000;
    final high = data?['blueprint_max'] as int? ?? 253999;
    final used = usedIDs.toSet();
    for (var id = low; id <= high; id++) {
      if (!used.contains(id)) return id;
    }
    return low;
  }

  List<Map<String, String>> comboChain = [];
  List<Map<String, String>> comboDeadEnds = [];
  /// 动作块内的帧级按键切换（CustomStateSwitch），第二条连招通道。
  /// 这份是渲染后的现状（含已保存的编辑），编辑器据此展示。
  /// 元素里除了 state/next/keycode/window 这些展示字段，还有 attrs（原始属性表），
  /// 所以不能收窄成 Map<String, String>。
  List<Map<String, dynamic>> frameSwitches = [];
  /// 本次编辑中改过的状态 → 该状态要写的切换列表。
  Map<String, List<Map<String, dynamic>>> frameEdits = {};
  /// 后端已保存的帧级连招（用于判断某状态是否被定制过）。
  Map<String, List<Map<String, dynamic>>> frameSaved = {};
  /// 底层按键码可选项（后端 frame_keys；与 delayacttable 的按键编号是两套）。
  List<Map<String, String>> frameKeys = [];
  bool frameEditing = false;

  /// 客户端 delayacttable.xml 的 <KeyInputList>：按键编号不是连续的
  /// （1..6 基础键、8..13 双键组合、19..24 方向组合、31..33 站/跑/跳技），
  /// 所以只认客户端自己的表，不能写死。
  List<Map<String, String>> comboKeys = [];
  bool chainEditing = false;
  List<Map<String, String>> chainDraft = [];
  String? chainOld, chainKey, chainNew;
  String? addStatePick;

  /// 连招限制（comborule.xml）的当前视图：某一招最多命中几次，以及
  /// 「两招不能连」（黑名单）/「只能接指定招」（白名单）。
  /// 它不是连招链的替代品——连招链决定按键能不能推到下一段，这里决定推到
  /// 下一段之后还能不能打中。规则里的编号是动作块的被动编号
  /// （skillproid），不是状态号，所以下拉选项必须来自服务端解析结果。
  Map<String, dynamic> comboRuleInfo = {};
  bool comboRuleEditing = false;
  List<Map<String, dynamic>> comboRuleMaxDraft = [];
  List<Map<String, dynamic>> comboRuleBlackDraft = [];
  List<Map<String, dynamic>> comboRuleWhiteDraft = [];
  /// 下拉框用 initialValue 只在创建时生效，草稿整体换掉时必须换 key，
  /// 否则取消编辑后界面还留着被丢弃的选择。
  int comboRuleVersion = 0;
  /// 读取失败的原因。以前这里静默清空 comboRuleInfo，卡片直接消失，看起来
  /// 和「这把武器没有限制」一模一样——后端还是旧二进制（不认识
  /// weapon_combo_rule）时会这样，极难自查。现在把原因留在卡片上。
  String comboRuleFailure = '';

  /// 兜底按键表：客户端表读不出来时用（编号与 delayacttable.xml 注释一致）。
  static const chainKeys = [
    {'v': '1', 'l': '普通攻击'},
    {'v': '2', 'l': '特殊攻击'},
    {'v': '3', 'l': '瞄准'},
    {'v': '4', 'l': '跳跃'},
    {'v': '5', 'l': '前'},
    {'v': '6', 'l': '后'},
    {'v': '8', 'l': 'Z+Z'},
    {'v': '9', 'l': 'C+C'},
    {'v': '10', 'l': 'X+X'},
    {'v': '11', 'l': 'X+C'},
    {'v': '12', 'l': 'Z+X+C'},
    {'v': '13', 'l': 'C+X'},
    {'v': '19', 'l': '前前普通'},
    {'v': '20', 'l': '前前特殊'},
    {'v': '21', 'l': '前特殊'},
    {'v': '22', 'l': '前普通'},
    {'v': '23', 'l': '后特殊'},
    {'v': '24', 'l': '后普通'},
    {'v': '31', 'l': '站技 Z+X+C'},
    {'v': '32', 'l': '跑技 前前C+X'},
    {'v': '33', 'l': '跳技 跳C+X'},
  ];

  /// 可用按键：优先客户端表。
  List<Map<String, String>> get usableKeys =>
      comboKeys.isNotEmpty ? comboKeys : chainKeys;

  Future<void> refreshChain(dynamic weaponId) async {
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({'operation': 'weapon_combo_chain', 'weapon': weaponId}),
      );
      if (!mounted) return;
      setState(() {
        comboChain = [
          for (final e in (result['chain'] as List? ?? []))
            Map<String, String>.from(e as Map),
        ];
        comboDeadEnds = [
          for (final e in (result['dead_ends'] as List? ?? []))
            Map<String, String>.from(e as Map),
        ];
        frameSwitches = [
          for (final e in (result['frame_switches'] as List? ?? []))
            Map<String, dynamic>.from(e as Map),
        ];
        frameSaved = {
          for (final e in (result['frame_switches_saved'] as Map? ?? {}).entries)
            '${e.key}': [
              for (final sw in (e.value as List? ?? []))
                Map<String, dynamic>.from(sw as Map),
            ],
        };
        frameKeys = [
          for (final e in (result['frame_keys'] as List? ?? []))
            {'v': '${(e as Map)['value']}', 'l': '${e['label']}'},
        ];
        frameEdits = {};
        frameEditing = false;
        comboKeys = [
          for (final e in (result['keys'] as List? ?? []))
            {'v': '${(e as Map)['id']}', 'l': '${e['label']}'},
        ];
      });
    } catch (_) {
      if (mounted) {
        setState(() => comboChain = []);
        setState(() => comboDeadEnds = []);
        setState(() => frameSwitches = []);
        setState(() {
          frameSaved = {};
          frameEdits = {};
          frameEditing = false;
        });
      }
    }
  }

  String chainStateLabel(String state) {
    for (final w in (data?['weapons'] as List? ?? [])) {
      if (w['id'] != weapon!['id']) continue;
      for (final s in (w['stages'] as List? ?? [])) {
        if ('${s['state']}' == state && '${s['label'] ?? ''}'.isNotEmpty) {
          return '$state · ${s['label']}';
        }
      }
    }
    return state;
  }

  void startChainEdit() {
    setState(() {
      chainDraft = [for (final e in comboChain) Map<String, String>.from(e)];
      chainEditing = true;
      chainOld = chainKey = chainNew = null;
    });
  }

  void cancelChainEdit() {
    setState(() {
      chainEditing = false;
      chainDraft = [];
    });
  }

  /// 该武器有帧级连招（或被定制过）的状态，按状态号排序。
  List<String> get frameStates {
    final states = <String>{
      ...frameEdits.keys,
      ...frameSaved.keys,
      for (final f in frameSwitches) '${f['state']}',
    }.toList();
    states.sort(
      (a, b) => (int.tryParse(a) ?? 0).compareTo(int.tryParse(b) ?? 0),
    );
    return states;
  }

  /// 某状态当前要展示的切换列表：改过用改动，否则用渲染后的现状。
  List<Map<String, dynamic>> frameListFor(String state) {
    final edit = frameEdits[state];
    if (edit != null) return edit;
    return [
      for (final f in frameSwitches)
        if ('${f['state']}' == state) Map<String, dynamic>.from(f),
    ];
  }

  /// 某状态是否被本次编辑动过（动过就标出来，免得以为改动丢了）。
  bool frameTouched(String state) => frameEdits.containsKey(state);

  bool get frameDirty => frameEdits.isNotEmpty;

  void startFrameEdit() {
    setState(() {
      frameEdits = {};
      frameEditing = true;
    });
  }

  void cancelFrameEdit() {
    setState(() {
      frameEdits = {};
      frameEditing = false;
    });
  }

  void frameRemove(String state, int index) {
    final list = [for (final e in frameListFor(state)) e];
    if (index < 0 || index >= list.length) return;
    list.removeAt(index);
    setState(() => frameEdits[state] = list);
  }

  Future<void> frameAdd(String state) async {
    final added = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _FrameSwitchDialog(
        state: state,
        states: [
          for (final s in (data?['states'] as List? ?? [])) '$s',
        ],
        keys: frameKeys,
      ),
    );
    if (added == null || !mounted) return;
    final list = [for (final e in frameListFor(state)) e]..add(added);
    setState(() => frameEdits[state] = list);
  }

  /// 保存：整把武器一次性替换。没动过的状态原样带上，后端按 map 里有什么写什么。
  Future<void> saveFrameSwitches() async {
    final payload = <String, dynamic>{};
    for (final entry in frameSaved.entries) {
      payload[entry.key] = _encodeFrameList(entry.value);
    }
    for (final entry in frameEdits.entries) {
      payload[entry.key] = _encodeFrameList(entry.value);
    }
    final prefer = weapon!['id'] as int?;
    setState(() {
      busy = true;
      failed = false;
      message = '正在保存帧级连招…';
    });
    try {
      final result = Map<String, dynamic>.from(await widget.api({
        'operation': 'weapon_frame_switch_set',
        'weapon': weapon!['id'],
        'frame_switches': payload,
      }));
      if (!mounted) return;
      setState(() {
        busy = false;
        frameEditing = false;
        frameEdits = {};
        message = '${result['message'] ?? '已保存帧级连招'}';
      });
      await load(prefer: prefer);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  /// 清掉这把武器的全部帧级连招定制，动作块回到原样。
  Future<void> clearFrameSwitches() async {
    final prefer = weapon!['id'] as int?;
    setState(() {
      busy = true;
      failed = false;
      message = '正在清除帧级连招定制…';
    });
    try {
      final result = Map<String, dynamic>.from(await widget.api({
        'operation': 'weapon_frame_switch_set',
        'weapon': weapon!['id'],
        'frame_switches': <String, dynamic>{},
      }));
      if (!mounted) return;
      setState(() {
        busy = false;
        frameEditing = false;
        frameEdits = {};
        message = '${result['message'] ?? '已清除'}';
      });
      await load(prefer: prefer);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  List<Map<String, dynamic>> _encodeFrameList(
    List<Map<String, dynamic>> list,
  ) => [
    for (final item in list)
      {
        'attrs': [
          for (final attr in (item['attrs'] as List? ?? []))
            {
              'key': '${(attr as Map)['key']}',
              'value': '${attr['value']}',
            },
        ],
      },
  ];

  Future<void> saveChain() async {
    final prefer = weapon!['id'] as int?;
    setState(() {
      busy = true;
      failed = false;
      message = '正在保存连招链…';
    });
    try {
      final result = Map<String, dynamic>.from(await widget.api({
        'operation': 'weapon_combo_chain_set',
        'weapon': weapon!['id'],
        'transitions': [
          for (final e in chainDraft)
            {'old': e['old'], 'new': e['new'], 'key': e['key']},
        ],
      }));
      if (!mounted) return;
      setState(() {
        busy = false;
        chainEditing = false;
        message = '${result['message'] ?? '已保存'}';
      });
      await load(prefer: prefer);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  Future<void> clearChain() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('清除定制的连招链'),
        content: const Text('清空后恢复继承：借用供体（或客户端原生）的连招。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('清除'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    final prefer = weapon!['id'] as int?;
    setState(() {
      busy = true;
      failed = false;
      message = '正在清除连招链…';
    });
    try {
      final result = Map<String, dynamic>.from(await widget.api({
        'operation': 'weapon_combo_chain_set',
        'weapon': weapon!['id'],
        'transitions': const [],
      }));
      if (!mounted) return;
      setState(() {
        busy = false;
        chainEditing = false;
        message = '${result['message'] ?? '已清除'}';
      });
      await load(prefer: prefer);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  /// 读取某把武器在 comborule.xml 里的限制。只在选中武器时调用：
  /// 选项表（本武器动作块真正用到的被动编号）只有服务端解得出来。
  Future<void> refreshComboRule(dynamic weaponId) async {
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({'operation': 'weapon_combo_rule', 'weapon': weaponId}),
      );
      if (!mounted) return;
      setState(() {
        comboRuleInfo = result;
        comboRuleFailure = '';
        comboRuleEditing = false;
        syncComboRuleDraft();
      });
    } catch (error) {
      // 读不出来时不再静默隐藏：把原因显示在卡片上。否则「后端是旧二进制」
      // 和「这把武器本来就没有限制」在界面上完全一样，只能靠猜。
      if (mounted) {
        setState(() {
          comboRuleInfo = {};
          comboRuleFailure = '$error';
          comboRuleEditing = false;
          syncComboRuleDraft();
        });
      }
    }
  }

  String _comboText(dynamic value) => value == null ? '' : '$value';

  /// 把服务端返回的规则摊平进三份草稿。编辑自建武器时，官方已经写好的块
  /// 会作为起点（服务端只允许改写自建武器与未登记限制的武器）。
  void syncComboRuleDraft() {
    comboRuleVersion++;
    final rules = Map<String, dynamic>.from(
      comboRuleInfo['rules'] as Map? ?? const {},
    );
    comboRuleMaxDraft = [
      for (final e in (rules['max'] as List? ?? []))
        {
          'skill': _comboText(e['skill']),
          'max_combo': _comboText(e['max_combo']),
          'exceed_state': _comboText(e['exceed_state']),
          'exceed_skill_pro_id': _comboText(e['exceed_skill_pro_id']),
        },
    ];
    comboRuleBlackDraft = [
      for (final e in (rules['black'] as List? ?? []))
        {'prev': _comboText(e['prev']), 'cur': _comboText(e['cur'])},
    ];
    comboRuleWhiteDraft = [
      for (final e in (rules['white'] as List? ?? []))
        {'prev': _comboText(e['prev']), 'cur': _comboText(e['cur'])},
    ];
  }

  bool get comboRuleEditable => comboRuleInfo['editable'] == true;

  bool get comboRuleCustomised => comboRuleInfo['overridden'] == true;

  int get comboRuleCount =>
      comboRuleMaxDraft.length +
      comboRuleBlackDraft.length +
      comboRuleWhiteDraft.length;

  void startComboRuleEdit() {
    setState(() {
      comboRuleEditing = true;
    });
  }

  void cancelComboRuleEdit() {
    setState(() {
      comboRuleEditing = false;
      syncComboRuleDraft();
    });
  }

  /// 保存（或清空）本武器的连招限制。清空走同一接口：后端收到空规则集就
  /// 把编辑器自己写的那一块整体删掉，官方块仍然原样保留。
  Future<void> saveComboRule({bool clear = false}) async {
    final prefer = weapon!['id'] as int?;
    setState(() {
      busy = true;
      failed = false;
      message = clear ? '正在清除连招限制…' : '正在保存连招限制…';
    });
    try {
      final result = Map<String, dynamic>.from(await widget.api({
        'operation': 'weapon_combo_rule_set',
        'weapon': weapon!['id'],
        'combo_rule': clear
            ? const {'max': [], 'black': [], 'white': []}
            : {
                'max': [
                  for (final e in comboRuleMaxDraft)
                    {
                      'skill': e['skill'],
                      'max_combo': e['max_combo'],
                      'exceed_state': e['exceed_state'] ?? '',
                      'exceed_skill_pro_id': e['exceed_skill_pro_id'] ?? '',
                    },
                ],
                'black': [
                  for (final e in comboRuleBlackDraft)
                    {'prev': e['prev'], 'cur': e['cur']},
                ],
                'white': [
                  for (final e in comboRuleWhiteDraft)
                    {'prev': e['prev'], 'cur': e['cur']},
                ],
              },
      }));
      if (!mounted) return;
      setState(() {
        busy = false;
        comboRuleEditing = false;
        message = '${result['message'] ?? '已保存'}';
      });
      await load(prefer: prefer);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  /// 招式下拉选项。编号不是状态号也不是武器号，而是动作块里 <Anm> 上的
  /// skillproid；官方数据里还有一批编号在本武器的动作块里找不到（多半是
  /// 老版本留下的），它们必须照原样出现在下拉里，否则一是会显示成空值，
  /// 二是用户一保存就悄悄改掉了官方语义。
  List<DropdownMenuItem<String>> comboRuleSkillItems(Set<String> extra) {
    final items = <Map<String, String>>[
      for (final o in (comboRuleInfo['skills'] as List? ?? []))
        {
          'v': '${o['skill']}',
          'l': '${o['skill']} · ${o['state']} · ${o['label']}',
          'known': '1',
        },
    ];
    final seen = {for (final o in items) o['v']!};
    for (final value in extra) {
      if (value.isEmpty || seen.contains(value)) continue;
      seen.add(value);
      items.add({'v': value, 'l': '$value ⚠ 本武器动作块没有引用', 'known': '0'});
    }
    return [
      for (final o in items)
        DropdownMenuItem(
          value: o['v'],
          child: Text(
            o['l']!,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(
              fontSize: 12,
              color: o['known'] == '1' ? null : Colors.deepOrange.shade800,
            ),
          ),
        ),
    ];
  }

  Set<String> get comboRuleUsedSkills => {
    for (final e in comboRuleMaxDraft) ...[
      _comboText(e['skill']),
      _comboText(e['exceed_skill_pro_id']),
    ],
    for (final e in [...comboRuleBlackDraft, ...comboRuleWhiteDraft]) ...[
      _comboText(e['prev']),
      _comboText(e['cur']),
    ],
  };

  /// 官方数据里编号对不上本武器动作块的条目，服务端会列出来。只提示、
  /// 不阻止保存：纪念版/克隆武器沿用供体编号就是这种情况，是合法的。
  List<String> get comboRuleUnknownSkills => [
    for (final v in (comboRuleInfo['unknown_skills'] as List? ?? [])) '$v',
  ];

  /// 连招链：按「老状态」分组展示 delayacttable.xml 的状态转移，可编辑。
  /// 自建武器即使一条转移都没有也要显示这张卡片，否则没法从零开始编连招。
  Widget comboChainCard() {
    final created = weapon != null && isCreated(weapon!['id']);
    if (comboChain.isEmpty && frameSwitches.isEmpty && !chainEditing && !created) {
      return const SizedBox.shrink();
    }
    final states = [for (final s in (data?['states'] as List? ?? [])) '$s'];
    final editing = chainEditing;
    final rows = editing ? chainDraft : comboChain;
    final byOld = <String, List<Map<String, String>>>{};
    final order = <String>[];
    for (final e in rows) {
      final o = e['old']!;
      if (!byOld.containsKey(o)) {
        byOld[o] = [];
        order.add(o);
      }
      byOld[o]!.add(e);
    }
    order.sort((a, b) => int.parse(a).compareTo(int.parse(b)));
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.account_tree_outlined, size: 18),
                const SizedBox(width: 8),
                Text(
                  '连招链 · ${editing ? chainDraft.length : comboChain.length} 条转移',
                  style: const TextStyle(fontWeight: FontWeight.bold),
                ),
                const SizedBox(width: 8),
                const Expanded(
                  child: Text(
                    '站在左侧状态上按下对应键，就切到右侧状态',
                    style: TextStyle(fontSize: 11, color: Colors.black54),
                  ),
                ),
                if (!editing)
                  TextButton.icon(
                    onPressed: busy ? null : startChainEdit,
                    icon: const Icon(Icons.edit, size: 16),
                    label: const Text('编辑'),
                  ),
                if (editing) ...[
                  TextButton(
                    onPressed: busy ? null : cancelChainEdit,
                    child: const Text('取消'),
                  ),
                  FilledButton.icon(
                    onPressed: busy ? null : saveChain,
                    icon: const Icon(Icons.check, size: 16),
                    label: const Text('保存连招链'),
                  ),
                ],
              ],
            ),
            const SizedBox(height: 6),
            if (!editing && rows.isEmpty)
              const Padding(
                padding: EdgeInsets.only(bottom: 6),
                child: Text(
                  '还没有连招链：点「编辑」添加「老状态 → 按键 → 新状态」的转移。'
                  '没有转移时，按键推不到下一段（第一下能出、之后卡住）。',
                  style: TextStyle(fontSize: 12, color: Colors.deepOrange),
                ),
              ),
            for (final o in order) comboChainGroup(o, byOld[o]!, editing),
            if (editing) chainAddRow(states),
            if (!editing && comboDeadEnds.isNotEmpty) deadEndNotice(),
            if (!editing && comboChain.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 4),
                child: TextButton(
                  onPressed: busy ? null : clearChain,
                  child: const Text(
                    '恢复继承（清空定制，回到借用供体/原生）',
                    style: TextStyle(fontSize: 12),
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }

  /// 连招限制读不出来时的占位卡片。只提示、不挡住别的卡片；原因原文放在
  /// 下面，方便直接看出是「exe 旁边的后端还是旧版本」还是别的报错。
  Widget comboRuleUnavailable() {
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.rule_folder_outlined, size: 18),
                const SizedBox(width: 8),
                const Text(
                  '连招限制 · 读取失败',
                  style: TextStyle(fontWeight: FontWeight.bold),
                ),
              ],
            ),
            const SizedBox(height: 6),
            const Text(
              '读不到这把武器的连招限制表。刚更新过 GM 的话，先完全关闭再重新打开'
              '（旧实例仍在跑启动时加载的那份代码）；也可能是 GM 目录旁边的 '
              'kungfu-desktop-admin.exe 还是旧版本。',
              style: TextStyle(fontSize: 12, color: Colors.deepOrange),
            ),
            const SizedBox(height: 4),
            SelectableText(
              comboRuleFailure,
              style: const TextStyle(fontSize: 11, color: Colors.black54),
            ),
          ],
        ),
      ),
    );
  }

  /// 连招限制（comborule.xml）卡片。
  ///
  /// 和连招链的分工值得写在卡片上：连招链决定「按下这个键能不能推到下一段」，
  /// 这张表决定「推过去之后还能不能打中」。玩家反馈的「第二下挥空、没有伤害」
  /// 通常出在这里——某一招的命中次数用完了。
  ///
  /// 规则里的编号是动作块的 skillproid，既不是状态号也不是武器号，所以只给
  /// 下拉、不给手填：手填一个客户端根本不引用的编号，规则会静默失效。
  Widget comboRuleCard() {
    if (comboRuleInfo.isEmpty && comboRuleFailure.isEmpty) {
      return const SizedBox.shrink();
    }
    if (comboRuleInfo.isEmpty) return comboRuleUnavailable();
    final editing = comboRuleEditing;
    final editable = comboRuleEditable;
    final skillItems = comboRuleSkillItems(comboRuleUsedSkills);
    // 两种模式共用同一份草稿：只读时它就是从服务端同步下来的规则，编辑时
    // 它才是真正的草稿。分开渲染就会出现「有官方条目却显示（无）」这类偏差。
    final maxRows = comboRuleMaxDraft;
    final blackRows = comboRuleBlackDraft;
    final whiteRows = comboRuleWhiteDraft;
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.rule_folder_outlined, size: 18),
                const SizedBox(width: 8),
                Text(
                  '连招限制 · $comboRuleCount 条',
                  style: const TextStyle(fontWeight: FontWeight.bold),
                ),
                const SizedBox(width: 8),
                const Expanded(
                  child: Text(
                    '一招最多命中几次 / 两招不能连 / 只能接指定招',
                    style: TextStyle(fontSize: 11, color: Colors.black54),
                  ),
                ),
                if (!editing && editable)
                  TextButton.icon(
                    onPressed: busy ? null : startComboRuleEdit,
                    icon: const Icon(Icons.edit, size: 16),
                    label: const Text('编辑'),
                  ),
                if (editing) ...[
                  TextButton(
                    onPressed: busy ? null : cancelComboRuleEdit,
                    child: const Text('取消'),
                  ),
                  FilledButton.icon(
                    onPressed: busy ? null : () => saveComboRule(),
                    icon: const Icon(Icons.check, size: 16),
                    label: const Text('保存限制'),
                  ),
                ],
              ],
            ),
            const SizedBox(height: 6),
            if (!editable)
              comboRuleNotice(
                '本客户端已内置这把武器的连招限制（官方数据），编辑器只读。'
                '下面是游戏当前真正生效的条目。',
                Colors.blueGrey,
              ),
            if (comboRuleUnknownSkills.isNotEmpty)
              comboRuleNotice(
                '这些编号在本武器的动作块里找不到：${comboRuleUnknownSkills.join('、')}。'
                '克隆/纪念版武器沿用供体编号属正常；若是自己填错的，这条规则不会生效。',
                Colors.orange,
              ),
            if (!editing && comboRuleCount == 0)
              const Padding(
                padding: EdgeInsets.only(bottom: 4),
                child: Text(
                  '没有限制：本武器所有招式的命中次数都不受这张表约束。',
                  style: TextStyle(fontSize: 12, color: Colors.black54),
                ),
              ),
            comboRuleGroup(
              title: '命中上限',
              hint: '某一招打满几次后不再命中',
              editing: editing,
              count: maxRows.length,
              onAdd: () => setState(() => comboRuleMaxDraft.add({
                'skill': '',
                'max_combo': '1',
                'exceed_state': '',
                'exceed_skill_pro_id': '',
              })),
              rows: [
                if (editing)
                  for (var i = 0; i < maxRows.length; i++)
                    comboRuleMaxRow(i, maxRows[i], skillItems),
                if (!editing)
                  for (final e in maxRows) comboRuleBullet(comboRuleMaxSummary(e)),
              ],
            ),
            comboRuleGroup(
              title: '黑名单',
              hint: '这两招不能连着出；前后填同一个 = 禁止连续放同一招',
              editing: editing,
              count: blackRows.length,
              onAdd: () => setState(
                () => comboRuleBlackDraft.add({'prev': '', 'cur': ''}),
              ),
              rows: [
                if (editing)
                  for (var i = 0; i < blackRows.length; i++)
                    comboRuleLinkRow(
                      'black',
                      i,
                      blackRows,
                      blackRows[i],
                      skillItems,
                    ),
                if (!editing)
                  for (final e in blackRows)
                    comboRuleBullet(
                      '不能连：${_comboText(e['prev'])} → ${_comboText(e['cur'])}',
                    ),
              ],
            ),
            comboRuleGroup(
              title: '白名单',
              hint: '前一招之后只能接这几招',
              editing: editing,
              count: whiteRows.length,
              onAdd: () => setState(
                () => comboRuleWhiteDraft.add({'prev': '', 'cur': ''}),
              ),
              rows: [
                if (editing)
                  for (var i = 0; i < whiteRows.length; i++)
                    comboRuleLinkRow(
                      'white',
                      i,
                      whiteRows,
                      whiteRows[i],
                      skillItems,
                    ),
                if (!editing)
                  for (final e in whiteRows)
                    comboRuleBullet(
                      '只能接：${_comboText(e['prev'])} → ${_comboText(e['cur'])}',
                    ),
              ],
            ),
            if (!editing && editable && comboRuleCustomised)
              Padding(
                padding: const EdgeInsets.only(top: 4),
                child: TextButton(
                  onPressed: busy ? null : () => saveComboRule(clear: true),
                  child: const Text(
                    '清除定制（回到客户端原样）',
                    style: TextStyle(fontSize: 12),
                  ),
                ),
              ),
            if (!editing && editable && !comboRuleCustomised)
              const Padding(
                padding: EdgeInsets.only(top: 4),
                child: Text(
                  '保存后需要点「应用到游戏」才会写进配置包；那一步会连带把本武器'
                  '「连招与命中效果」里已保存的伤害方案一起写入。',
                  style: TextStyle(fontSize: 11, color: Colors.black54),
                ),
              ),
          ],
        ),
      ),
    );
  }

  Widget comboRuleNotice(String text, MaterialColor tone) {
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(bottom: 6),
      padding: const EdgeInsets.all(8),
      decoration: BoxDecoration(
        color: tone.shade50,
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(
        text,
        style: TextStyle(fontSize: 12, color: tone.shade900),
      ),
    );
  }

  Widget comboRuleBullet(String text) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 1),
      child: Text(
        '· $text',
        style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
      ),
    );
  }

  String comboRuleMaxSummary(Map e) {
    var text =
        '${_comboText(e['skill'])} → 最多命中 ${_comboText(e['max_combo'])} 次';
    final state = _comboText(e['exceed_state']);
    final skill = _comboText(e['exceed_skill_pro_id']);
    if (state.isNotEmpty) text += '；超出后改用状态 $state';
    if (skill.isNotEmpty) text += ' / 被动 $skill';
    return text;
  }

  /// 三组条目共用的外壳：标题 + 说明 + 添加按钮 + 行。
  Widget comboRuleGroup({
    required String title,
    required String hint,
    required bool editing,
    required int count,
    required VoidCallback onAdd,
    required List<Widget> rows,
  }) {
    return Padding(
      padding: const EdgeInsets.only(top: 6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(
                title,
                style: const TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  hint,
                  style: const TextStyle(fontSize: 11, color: Colors.black54),
                ),
              ),
              if (editing)
                TextButton.icon(
                  onPressed: busy ? null : onAdd,
                  icon: const Icon(Icons.add, size: 16),
                  label: const Text('添加', style: TextStyle(fontSize: 12)),
                ),
            ],
          ),
          if (!editing && count == 0)
            const Text(
              '· （无）',
              style: TextStyle(fontSize: 12, color: Colors.black54),
            ),
          ...rows,
        ],
      ),
    );
  }

  Widget comboRuleMaxRow(
    int index,
    Map<String, dynamic> row,
    List<DropdownMenuItem<String>> skillItems,
  ) {
    final skill = _comboText(row['skill']);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: DropdownButtonFormField<String>(
                  key: ValueKey('crf-max-skill-$comboRuleVersion-$index'),
                  initialValue: skill.isEmpty ? null : skill,
                  isExpanded: true,
                  decoration: const InputDecoration(
                    labelText: '招式（动作块被动编号）',
                    isDense: true,
                  ),
                  items: skillItems,
                  onChanged: (v) => setState(() => row['skill'] = v ?? ''),
                ),
              ),
              IconButton(
                onPressed: busy
                    ? null
                    : () => setState(() => comboRuleMaxDraft.removeAt(index)),
                icon: const Icon(Icons.close, size: 16, color: Colors.deepOrange),
                tooltip: '删除这一条',
              ),
            ],
          ),
          Padding(
            padding: const EdgeInsets.only(left: 4, bottom: 2),
            child: Wrap(
              spacing: 8,
              runSpacing: 4,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                SizedBox(
                  width: 112,
                  child: TextFormField(
                    key: ValueKey('crf-max-count-$comboRuleVersion-$index'),
                    initialValue: _comboText(row['max_combo']),
                    keyboardType: TextInputType.number,
                    inputFormatters: [FilteringTextInputFormatter.digitsOnly],
                    decoration: const InputDecoration(
                      labelText: '最多命中',
                      isDense: true,
                    ),
                    onChanged: (v) => row['max_combo'] = v,
                  ),
                ),
                SizedBox(
                  width: 140,
                  child: TextFormField(
                    key: ValueKey('crf-max-state-$comboRuleVersion-$index'),
                    initialValue: _comboText(row['exceed_state']),
                    keyboardType: TextInputType.number,
                    inputFormatters: [
                      FilteringTextInputFormatter.digitsOnly,
                      LengthLimitingTextInputFormatter(4),
                    ],
                    decoration: const InputDecoration(
                      labelText: '超出后状态',
                      hintText: '可空',
                      isDense: true,
                    ),
                    onChanged: (v) => row['exceed_state'] = v,
                  ),
                ),
                SizedBox(
                  width: 240,
                  child: DropdownButtonFormField<String>(
                    key: ValueKey('crf-max-exceed-$comboRuleVersion-$index'),
                    initialValue: _comboText(row['exceed_skill_pro_id']),
                    isExpanded: true,
                    decoration: const InputDecoration(
                      labelText: '超出后改用',
                      isDense: true,
                    ),
                    items: [
                      const DropdownMenuItem(
                        value: '',
                        child: Text('（不指定）', style: TextStyle(fontSize: 12)),
                      ),
                      ...skillItems,
                    ],
                    onChanged: (v) =>
                        setState(() => row['exceed_skill_pro_id'] = v ?? ''),
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget comboRuleLinkRow(
    String kind,
    int index,
    List<Map<String, dynamic>> rows,
    Map<String, dynamic> row,
    List<DropdownMenuItem<String>> skillItems,
  ) {
    final prev = _comboText(row['prev']);
    final cur = _comboText(row['cur']);
    // 白名单的"后一招"允许为空：含义是这一招之后什么都不许接（官方 253147 就
    // 靠它实现 ZC/ZX 只能接爆气）。不加这个选项，那种数据在编辑态会显示成
    // "未选择"，看着像丢数据。
    final curItems = kind == 'white'
        ? <DropdownMenuItem<String>>[
            const DropdownMenuItem<String>(
              value: '',
              child: Text('（空）之后不许接任何招'),
            ),
            ...skillItems,
          ]
        : skillItems;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        children: [
          Expanded(
            child: DropdownButtonFormField<String>(
              key: ValueKey('crf-$kind-prev-$comboRuleVersion-$index'),
              initialValue: prev.isEmpty ? null : prev,
              isExpanded: true,
              decoration: const InputDecoration(
                labelText: '前一招',
                isDense: true,
              ),
              items: skillItems,
              onChanged: (v) => setState(() => row['prev'] = v ?? ''),
            ),
          ),
          const Padding(
            padding: EdgeInsets.symmetric(horizontal: 4),
            child: Icon(Icons.arrow_forward, size: 14),
          ),
          Expanded(
            child: DropdownButtonFormField<String>(
              key: ValueKey('crf-$kind-cur-$comboRuleVersion-$index'),
              initialValue: cur.isEmpty
                  ? (kind == 'white' ? '' : null)
                  : cur,
              isExpanded: true,
              decoration: const InputDecoration(
                labelText: '后一招',
                isDense: true,
              ),
              items: curItems,
              onChanged: (v) => setState(() => row['cur'] = v ?? ''),
            ),
          ),
          IconButton(
            onPressed: busy ? null : () => setState(() => rows.removeAt(index)),
            icon: const Icon(Icons.close, size: 16, color: Colors.deepOrange),
            tooltip: '删除这一条',
          ),
        ],
      ),
    );
  }

  /// 断链提示：能被打进（有入边）但没有任何出边、连到这里就停的状态。
  /// 有些是设计上的收招/硬直终点，不一定是缺陷；这里只负责把它们显式列出来。
  Widget deadEndNotice() {
    return Padding(
      padding: const EdgeInsets.only(top: 6),
      child: Container(
        width: double.infinity,
        padding: const EdgeInsets.all(8),
        decoration: BoxDecoration(
          color: Colors.orange.shade50,
          borderRadius: BorderRadius.circular(6),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.warning_amber,
                    size: 16, color: Colors.deepOrange.shade700),
                const SizedBox(width: 6),
                Text(
                  '断链（连到这里就不能继续连）',
                  style: TextStyle(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                    color: Colors.deepOrange.shade800,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 4),
            Wrap(
              spacing: 8,
              runSpacing: 4,
              children: [
                for (final d in comboDeadEnds)
                  Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 8, vertical: 3),
                    decoration: BoxDecoration(
                      color: Colors.orange.shade100,
                      borderRadius: BorderRadius.circular(6),
                    ),
                    child: Text(
                      d['label'] ?? d['state'] ?? '',
                      style: TextStyle(
                        fontFamily: 'monospace',
                        fontSize: 12,
                        color: Colors.deepOrange.shade900,
                      ),
                    ),
                  ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  /// 帧级按键切换（动作块内 CustomStateSwitch）：第二条连招通道，可增可删。
  /// 动作块被别的武器共用时会先克隆成这把武器独占的块，所以增删不影响原武器。
  Widget frameSwitchCard() {
    final states = frameStates;
    final hasEdit = frameSaved.isNotEmpty;
    return Padding(
      padding: const EdgeInsets.only(top: 6),
      child: Container(
        width: double.infinity,
        padding: const EdgeInsets.all(8),
        decoration: BoxDecoration(
          color: Colors.blueGrey.shade50,
          borderRadius: BorderRadius.circular(6),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.animation, size: 16, color: Colors.blueGrey.shade700),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    frameEditing
                        ? '帧级按键切换 · 编辑中'
                            '${frameEdits.isEmpty ? '' : '（改过 ${frameEdits.length} 个状态）'}'
                        : '帧级按键切换 · ${frameSwitches.length} 条',
                    style: TextStyle(
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                      color: Colors.blueGrey.shade800,
                    ),
                  ),
                ),
                if (frameEditing) ...[
                  TextButton(
                    onPressed: busy ? null : cancelFrameEdit,
                    child: const Text('取消'),
                  ),
                  FilledButton.icon(
                    onPressed: busy ? null : saveFrameSwitches,
                    icon: const Icon(Icons.check, size: 16),
                    label: const Text('保存帧级连招'),
                  ),
                ] else
                  TextButton.icon(
                    onPressed: busy ? null : startFrameEdit,
                    icon: const Icon(Icons.edit, size: 15),
                    label: Text(states.isEmpty ? '添加' : '编辑'),
                  ),
              ],
            ),
            if (states.isEmpty) ...[
              const SizedBox(height: 2),
              Text(
                frameEditing
                    ? '点下面「给某个状态添加帧级连招」开始：播到第几帧按下哪个键 → 跳到哪个状态。'
                    : '动作块里没有帧级连招。连招也可能走上面那张连招链（delayacttable）表；'
                        '这里加的是动作播放中的按键切换。',
                style: const TextStyle(fontSize: 12, color: Colors.black54),
              ),
            ],
            for (final state in states) frameStateGroup(state),
            if (frameEditing) ...[
              const SizedBox(height: 2),
              TextButton.icon(
                onPressed: busy ? null : framePickStateAndAdd,
                icon: const Icon(Icons.add, size: 16),
                label: const Text(
                  '给某个状态添加帧级连招',
                  style: TextStyle(fontSize: 12),
                ),
              ),
            ],
            if (!frameEditing && hasEdit)
              TextButton(
                onPressed: busy ? null : clearFrameSwitches,
                child: const Text(
                  '清除本武器的帧级连招定制（动作块回到原样）',
                  style: TextStyle(fontSize: 12),
                ),
              ),
          ],
        ),
      ),
    );
  }

  Widget frameStateGroup(String state) {
    final list = frameListFor(state);
    final touched = frameTouched(state);
    return Padding(
      padding: const EdgeInsets.only(top: 6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(
                chainStateLabel(state),
                style: TextStyle(
                  fontFamily: 'monospace',
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  color: Colors.blueGrey.shade800,
                ),
              ),
              if (touched) ...[
                const SizedBox(width: 6),
                const Text(
                  '已改动',
                  style: TextStyle(fontSize: 11, color: Colors.teal),
                ),
              ],
            ],
          ),
          if (list.isEmpty)
            const Padding(
              padding: EdgeInsets.only(left: 10, top: 2),
              child: Text(
                '（保存后这个状态不再有帧级连招）',
                style: TextStyle(fontSize: 11, color: Colors.deepOrange),
              ),
            ),
          for (var index = 0; index < list.length; index++)
            Row(
              children: [
                const SizedBox(width: 10),
                Expanded(
                  child: Text(
                    frameSwitchLine(list[index]),
                    style: TextStyle(
                      fontFamily: 'monospace',
                      fontSize: 12,
                      color: Colors.blueGrey.shade900,
                    ),
                  ),
                ),
                if (frameEditing)
                  GestureDetector(
                    onTap: busy ? null : () => frameRemove(state, index),
                    child: const Padding(
                      padding: EdgeInsets.only(left: 6, right: 4),
                      child: Icon(
                        Icons.close,
                        size: 14,
                        color: Colors.deepOrange,
                      ),
                    ),
                  ),
              ],
            ),
          if (frameEditing)
            Padding(
              padding: const EdgeInsets.only(left: 4),
              child: TextButton.icon(
                onPressed: busy ? null : () => frameAdd(state),
                icon: const Icon(Icons.add, size: 15),
                label: const Text('添加到此状态', style: TextStyle(fontSize: 12)),
              ),
            ),
        ],
      ),
    );
  }

  /// 给还没有帧级连招的状态添加：先选状态。
  Future<void> framePickStateAndAdd() async {
    final all = [for (final s in (data?['states'] as List? ?? [])) '$s'];
    if (all.isEmpty) return;
    final picked = await showDialog<String>(
      context: context,
      builder: (_) => _SimplePickDialog(
        title: '为哪个状态添加帧级连招',
        hint: '搜索状态号',
        options: [
          for (final s in all) {'value': s, 'label': chainStateLabel(s)},
        ],
      ),
    );
    if (picked == null || !mounted) return;
    await frameAdd(picked);
  }

  /// 一条帧级连招的可读描述。
  String frameSwitchLine(Map<String, dynamic> item) {
    final attrs = <String, String>{
      for (final a in (item['attrs'] as List? ?? []))
        '${(a as Map)['key']}': '${a['value']}',
    };
    final key = attrs['keycode'] ?? '';
    final label = key.isEmpty ? '（自动）' : '按${frameKeyLabelOf(key)}';
    final start = attrs['switchstartframe'] ?? '?';
    final end = attrs['switchendframe'] ?? '?';
    final input = (attrs['inputstartframe'] ?? '').isEmpty
        ? ''
        : '，输入窗口 ${attrs['inputstartframe']}-${attrs['inputendframe']}';
    return '$label → ${attrs['nextstate'] ?? '?'}（第 $start-$end 帧$input）';
  }

  Widget comboChainGroup(
    String from,
    List<Map<String, String>> edges,
    bool editing,
  ) {
    final fromLabel = chainStateLabel(from);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 170,
            child: Text(
              fromLabel,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
            ),
          ),
          Expanded(
            child: Wrap(
              spacing: 8,
              runSpacing: 4,
              children: [
                for (var i = 0; i < edges.length; i++)
                  comboEdgeChip(edges[i], editing
                      ? () => setState(() => chainDraft.removeWhere(
                          (e) =>
                              identical(e, edges[i]) ||
                              (e['old'] == edges[i]['old'] &&
                                  e['new'] == edges[i]['new'] &&
                                  e['key'] == edges[i]['key']),
                        ))
                      : null),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget comboEdgeChip(Map<String, String> edge, VoidCallback? onDelete) {
    final text = '${edge['key_label'] ?? keyName(edge['key'] ?? '')} → ${edge['new']}';
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: Colors.teal.shade50,
        borderRadius: BorderRadius.circular(6),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(text, style: const TextStyle(fontFamily: 'monospace', fontSize: 12)),
          if (onDelete != null)
            GestureDetector(
              onTap: onDelete,
              child: const Padding(
                padding: EdgeInsets.only(left: 6),
                child: Icon(Icons.close, size: 14, color: Colors.deepOrange),
              ),
            ),
        ],
      ),
    );
  }

  String keyName(String key) {
    for (final k in usableKeys) {
      if (k['v'] == key) return '${k['l']}';
    }
    return '按键$key';
  }

  /// 新增转移。
  ///
  /// 这里刻意用 Wrap + 带文字按钮：左侧栏只有 560 宽，三个 150 的下拉框加一个
  /// 纯图标 IconButton 正好会顶到卡片右边缘，按钮被挤到裁剪区外就既看不见也点不到
  /// （用户看到的现象就是"连招链改了没作用"）。Wrap 会在必要时把按钮换到下一行，
  /// 带文字也保证它不依赖图标字体渲染出来。
  Widget chainAddRow(List<String> states) {
    final canAdd = chainOld != null && chainKey != null && chainNew != null;
    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Wrap(
            spacing: 8,
            runSpacing: 8,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              const Text('添加转移：', style: TextStyle(fontSize: 12)),
              SizedBox(
                width: 150,
                child: DropdownButtonFormField<String>(
                  key: ValueKey('chain-old-$chainEditing'),
                  initialValue: chainOld,
                  isExpanded: true,
                  decoration:
                      const InputDecoration(labelText: '老状态', isDense: true),
                  items: [
                    for (final s in states)
                      DropdownMenuItem(value: s, child: Text(s)),
                  ],
                  onChanged: (v) => setState(() => chainOld = v),
                ),
              ),
              SizedBox(
                width: 150,
                child: DropdownButtonFormField<String>(
                  key: ValueKey('chain-key-$chainEditing'),
                  initialValue: chainKey,
                  isExpanded: true,
                  decoration:
                      const InputDecoration(labelText: '按键', isDense: true),
                  // 带上编号：客户端表里有几个编号的注释是同一个按键序列
                  // （12 与 31 都是 Z+X+C），只显示注释会分不清。
                  items: [
                    for (final k in usableKeys)
                      DropdownMenuItem(
                        value: k['v'],
                        child: Text('${k['v']} · ${k['l']}'),
                      ),
                  ],
                  onChanged: (v) => setState(() => chainKey = v),
                ),
              ),
              SizedBox(
                width: 150,
                child: DropdownButtonFormField<String>(
                  key: ValueKey('chain-new-$chainEditing'),
                  initialValue: chainNew,
                  isExpanded: true,
                  decoration:
                      const InputDecoration(labelText: '新状态', isDense: true),
                  items: [
                    for (final s in states)
                      DropdownMenuItem(value: s, child: Text(s)),
                  ],
                  onChanged: (v) => setState(() => chainNew = v),
                ),
              ),
              FilledButton.icon(
                onPressed: canAdd
                    ? () {
                        setState(() {
                          chainDraft.add({
                            'old': chainOld!,
                            'new': chainNew!,
                            'key': chainKey!,
                            'key_label': keyName(chainKey!),
                          });
                          chainOld = chainKey = chainNew = null;
                        });
                      }
                    : null,
                icon: const Icon(Icons.add, size: 18),
                label: const Text('添加转移'),
              ),
            ],
          ),
          if (!canAdd)
            const Padding(
              padding: EdgeInsets.only(top: 6),
              child: Text(
                '三个都选好后「添加转移」才会亮起；按键只列出本客户端按键表里的编号。',
                style: TextStyle(fontSize: 11, color: Colors.black54),
              ),
            ),
        ],
      ),
    );
  }

  void select(Map<String, dynamic> value) {
    editorVersion++;
    selectedAction = null;
    weapon = value;
    final stored = data!['drafts']['${value['id']}'] as List? ?? [];
    rules = (value['stages'] as List).map((stage) {
      final saved = stored.where((r) => r['stage'] == stage['stage']);
      return saved.isEmpty
          ? <String, dynamic>{
              'stage': stage['stage'],
              'buff': 0,
              'level': 1,
              'duration': 3000,
            }
          : Map<String, dynamic>.from(saved.first);
    }).toList();
    dirty = false;
    refreshChain(value['id']);
    // 换武器时先清掉上一把的读取失败，免得旧报错挂在新武器上。
    comboRuleFailure = '';
    refreshComboRule(value['id']);
  }

  Future<bool> discard() async =>
      !dirty ||
      await showDialog<bool>(
            context: context,
            builder: (context) => AlertDialog(
              title: const Text('有未保存的修改'),
              content: const Text('离开后会丢弃当前修改。'),
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

  Future<void> execute(String operation) async {
    if (!(form.currentState?.validate() ?? false)) return;
    if (operation != 'weapon_save') {
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text(operation == 'weapon_apply' ? '应用到游戏' : '恢复原效果'),
          content: Text(
            operation == 'weapon_apply'
                ? '将应用「${weapon!['name']}」当前配置的招式伤害、BUFF 和受击效果。修改对使用此客户端的角色生效，不限当前账号。\n\n'
                    '写入 ${_clientInfo['directory'] ?? '当前客户端'}；写前自动备份。\n\n'
                    '请先退出游戏。重启后加载，实战效果尚待验证。'
                : '恢复「${weapon!['name']}」的原始招式伤害、BUFF 和受击效果，其他武器配置保留。请先退出游戏。',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('确认写入'),
            ),
          ],
        ),
      );
      if (confirmed != true || !mounted) return;
    }
    setState(() {
      busy = true;
      failed = false;
      message = '';
    });
    try {
      final result = await widget.api({
        'operation': operation,
        'weapon': weapon!['id'],
        'rules': rules,
        'revision': data!['revision'],
      });
      if (!mounted) return;
      setState(() {
        dirty = false;
        message = result['message'];
      });
      await load();
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  Future<void> publish() async {
    if (!(form.currentState?.validate() ?? false)) return;
    var notes = '${weapon!['name']}：';
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('更新到线上'),
        content: SizedBox(
          width: 560,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Text(
                '发布全部已保存的武器方案及当前编辑内容。将重启线上服务器，在线玩家会断开；玩家下次启动游戏时下载更新。不会覆盖本地客户端。',
              ),
              const SizedBox(height: 14),
              TextFormField(
                initialValue: notes,
                onChanged: (value) => notes = value,
                minLines: 4,
                maxLines: 8,
                maxLength: 2000,
                decoration: const InputDecoration(
                  labelText: '给玩家看的更新说明（武器名称、改动内容）',
                ),
              ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () {
              if (notes.trim().isNotEmpty) Navigator.pop(context, true);
            },
            child: const Text('发布到线上'),
          ),
        ],
      ),
    );
    final text = notes.trim();
    if (confirmed != true || !mounted) return;
    setState(() {
      busy = true;
      failed = false;
      message = '正在生成、上传并启用线上配置，请勿重复发布…';
    });
    try {
      final result = await widget.api({
        'operation': 'weapon_publish',
        'environment': 'online',
        'weapon': weapon!['id'],
        'rules': rules,
        'revision': data!['revision'],
        'notes': text,
      });
      if (!mounted) return;
      setState(() {
        message = result['message'];
        dirty = false;
      });
      await load();
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  /// 导出发版包。
  ///
  /// 产出的 zip 里路径就是客户端根目录下的相对路径（Data/config.spf2、
  /// Data/Weapon/Model/...），交给运维后**整包解压、覆盖到客户端根目录**即可，
  /// 不用挑文件、也不用知道哪个素材该放哪。
  ///
  /// 和「更新到线上」的区别：那个只把 Data/config.spf2 传上去，自制武器一旦
  /// 带自己的模型/贴图/动作，玩家端就会缺文件 —— 这个包解决的就是这件事。
  Future<void> exportPackage() async {
    if (!(form.currentState?.validate() ?? false)) return;
    final options = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _ExportDialog(
        weaponName: '${weapon!['name']}',
        createdCount: created.length,
      ),
    );
    if (options == null || !mounted) return;
    final include = [for (final s in (options['include'] as List? ?? [])) '$s'];
    if (include.isEmpty) return;
    setState(() {
      busy = true;
      failed = false;
      exportResult = {};
      message = '正在收集素材并打包，武器多、动作多时会慢一点…';
    });
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({
          'operation': 'weapon_package',
          'weapon': weapon!['id'],
          'all': options['all'] == true,
          'include': include,
          'applied_only': options['applied_only'] == true,
          'rules': rules,
          'revision': data!['revision'],
        }) as Map,
      );
      if (!mounted) return;
      setState(() {
        exportResult = result;
        busy = false;
        message = '已导出 ${result['name']}（${sizeText(result['size'])}，'
            '${(result['files'] as List? ?? []).length} 个素材文件）。'
            '把包解压后覆盖到客户端根目录即可。';
      });
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '导出失败：$e';
        });
      }
    }
  }

  static String sizeText(dynamic bytes) {
    final value = (bytes is num) ? bytes.toDouble() : 0.0;
    if (value >= 1024 * 1024) {
      return '${(value / 1024 / 1024).toStringAsFixed(2)} MB';
    }
    if (value >= 1024) return '${(value / 1024).toStringAsFixed(1)} KB';
    return '${value.toStringAsFixed(0)} B';
  }

  /// 导出合并包：只把「当前武器」（或全部自建武器）自己的配置条目和素材打成
  /// 一个 zip，导入时逐条合并进目标客户端，不整包覆盖。
  Future<void> exportMergePackage() async {
    final all = await showDialog<bool>(
      context: context,
      builder: (_) => _MergeExportDialog(
        weaponName: '${weapon!['name']}',
        createdCount: created.length,
      ),
    );
    if (all == null || !mounted) return;
    setState(() {
      busy = true;
      failed = false;
      exportResult = {};
      lastExportMerge = true;
      message = '正在提取「${weapon!['name']}」的配置条目并打包…';
    });
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({
          'operation': 'weapon_merge_export',
          'weapon': weapon!['id'],
          'all': all,
          'rules': rules,
          'revision': data!['revision'],
        }) as Map,
      );
      if (!mounted) return;
      setState(() {
        exportResult = result;
        busy = false;
        message = '已导出合并包 ${result['name']}（${sizeText(result['size'])}，'
            '${(result['files'] as List? ?? []).length} 个素材文件）。'
            '把它发到目标机器，在 GM 里点「导入武器包」选择该 zip 即可合并，'
            '目标客户端的其他配置不会被改动。';
      });
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '导出合并包失败：$e';
        });
      }
    }
  }

  /// 导入武器包：选一个合并包 zip，只合并包里武器的配置条目，其余条目不动。
  Future<void> importMergePackage() async {
    final source = await showDialog<String>(
      context: context,
      builder: (_) => _MergeImportDialog(
        loadPackages: () async => Map<String, dynamic>.from(
          await widget.api({'operation': 'weapon_merge_packages'}) as Map,
        ),
      ),
    );
    if (source == null || !mounted) return;
    // 先预览：以所选客户端的 config.spf2 为参照，列出包里哪些是新增、哪些是
    // 已存在会被覆盖的武器，用户确认后才真正写盘。
    setState(() {
      busy = true;
      failed = false;
      mergeImportResult = {};
      message = '正在分析合并包…';
    });
    Map<String, dynamic> preview;
    try {
      preview = Map<String, dynamic>.from(
        await widget.api({
          'operation': 'weapon_merge_preview',
          'source_path': source,
        }) as Map,
      );
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '读取合并包失败：$e';
        });
      }
      return;
    }
    if (!mounted) return;
    setState(() {
      busy = false;
      message = '';
    });
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => _MergePreviewDialog(preview: preview),
    );
    if (confirmed != true || !mounted) return;
    setState(() {
      busy = true;
      failed = false;
      message = '正在合并武器包…';
    });
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({
          'operation': 'weapon_merge_import',
          'source_path': source,
        }) as Map,
      );
      if (!mounted) return;
      setState(() {
        mergeImportResult = result;
        busy = false;
        final added = (result['new'] as List? ?? []).length;
        final changed = (result['modified'] as List? ?? []).length;
        final parts = <String>[];
        if (added > 0) parts.add('新增 $added 把');
        if (changed > 0) parts.add('覆盖已有 $changed 把');
        message = '导入完成：${parts.isEmpty ? '没有变化' : parts.join('、')}。'
            '共处理 ${(result['entries'] as List? ?? []).length} 个配置条目、'
            '解压 ${result['assets']} 个素材。'
            '目标客户端其他配置未被改动；改动前的备份见下方卡片。';
      });
      await load();
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '导入失败：$e';
        });
      }
    }
  }

  /// 发版包导出结果卡片：路径、分组统计、配置改动、文件清单。
  Widget exportCard() {
    final files = [
      for (final f in (exportResult['files'] as List? ?? []))
        Map<String, dynamic>.from(f as Map),
    ];
    final missing = [for (final m in (exportResult['missing'] as List? ?? [])) '$m'];
    final config = [
      for (final c in (exportResult['config_changes'] as List? ?? [])) '$c',
    ];
    final plans = [
      for (final p in (exportResult['plans'] as List? ?? []))
        Map<String, dynamic>.from(p as Map),
    ];
    final counts = Map<String, dynamic>.from(exportResult['counts'] as Map? ?? {});
    final byKind = <String, List<Map<String, dynamic>>>{};
    for (final file in files) {
      byKind.putIfAbsent('${file['kind']}', () => []).add(file);
    }
    final kinds = [for (final k in packageSections.keys) if (byKind.containsKey(k)) k];
    for (final k in byKind.keys) {
      if (!kinds.contains(k)) kinds.add(k);
    }
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.inventory_2_outlined, size: 18),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    '${lastExportMerge ? '合并包' : '发版包'} · ${exportResult['name']}',
                    style: const TextStyle(fontWeight: FontWeight.bold),
                  ),
                ),
                Text(
                  '${sizeText(exportResult['size'])} · ${files.length} 个素材',
                  style: const TextStyle(fontSize: 12, color: Colors.black54),
                ),
                // 结果卡片是常驻的，必须给一个关闭入口，否则只能靠下一次导出
                // 或重启 GM 才消失。
                IconButton(
                  tooltip: '关闭这张导出结果',
                  visualDensity: VisualDensity.compact,
                  icon: const Icon(Icons.close, size: 18),
                  onPressed: () => setState(() {
                    exportResult = {};
                    lastExportMerge = false;
                  }),
                ),
              ],
            ),
            const SizedBox(height: 6),
            SelectableText(
              '${exportResult['path']}',
              style: const TextStyle(fontSize: 11, color: Colors.black54),
            ),
            const SizedBox(height: 2),
            Text(
              'SHA256 ${exportResult['sha256']} · 生成于 ${exportResult['generated']}',
              style: const TextStyle(fontSize: 11, color: Colors.black54),
            ),
            const SizedBox(height: 8),
            Wrap(
              spacing: 6,
              runSpacing: 6,
              children: [
                for (final kind in kinds)
                  Chip(
                    visualDensity: VisualDensity.compact,
                    label: Text(
                      '${packageSections[kind] ?? kind} ${counts[kind] ?? byKind[kind]!.length}',
                      style: const TextStyle(fontSize: 11),
                    ),
                  ),
              ],
            ),
            if (missing.isNotEmpty) ...[
              const SizedBox(height: 8),
              Text(
                '有 ${missing.length} 个必需素材没找到，装到客户端后这把武器会显示异常：',
                style: const TextStyle(fontSize: 12, color: Colors.deepOrange),
              ),
              const SizedBox(height: 2),
              for (final m in missing)
                Text(
                  '  · $m',
                  style: const TextStyle(fontSize: 11, color: Colors.deepOrange),
                ),
            ],
            if (config.isNotEmpty) ...[
              const SizedBox(height: 8),
              const Text(
                '配置包里改动的文件（和客户端当前版本的差异）：',
                style: TextStyle(fontSize: 12),
              ),
              const SizedBox(height: 2),
              Wrap(
                spacing: 6,
                runSpacing: 4,
                children: [
                  for (final c in config)
                    Text(
                      c,
                      style: const TextStyle(fontSize: 11, color: Colors.black54),
                    ),
                ],
              ),
            ],
            if (plans.isNotEmpty) ...[
              const SizedBox(height: 8),
              Text(
                '包里带着方案的武器（配置是整包发的，别的武器已保存方案也会一起进包）：'
                '${plans.map((p) => p['name']).join('、')}',
                style: const TextStyle(fontSize: 11, color: Colors.black54),
              ),
            ],
            const SizedBox(height: 6),
            ExpansionTile(
              dense: true,
              tilePadding: EdgeInsets.zero,
              title: Text(
                '文件清单（${files.length}）',
                style: const TextStyle(fontSize: 12),
              ),
              children: [
                for (final kind in kinds) ...[
                  Padding(
                    padding: const EdgeInsets.only(top: 4, bottom: 2),
                    child: Text(
                      packageSections[kind] ?? kind,
                      style: const TextStyle(
                        fontSize: 11,
                        fontWeight: FontWeight.bold,
                      ),
                    ),
                  ),
                  for (final file in byKind[kind]!)
                    Padding(
                      padding: const EdgeInsets.only(left: 8, bottom: 1),
                      child: Text(
                        '${file['path']}  (${sizeText(file['size'])})',
                        style: const TextStyle(
                          fontSize: 11,
                          color: Colors.black54,
                        ),
                      ),
                    ),
                ],
              ],
            ),
          ],
        ),
      ),
    );
  }

  /// 合并导入结果卡片：包里哪些武器是新增、哪些覆盖了已有武器、逐条处理结果、备份路径。
  Widget mergeImportCard() {
    final entries = [
      for (final e in (mergeImportResult['entries'] as List? ?? []))
        Map<String, dynamic>.from(e as Map),
    ];
    final newWeapons = [
      for (final w in (mergeImportResult['new'] as List? ?? []))
        Map<String, dynamic>.from(w as Map),
    ];
    final modified = [
      for (final w in (mergeImportResult['modified'] as List? ?? []))
        Map<String, dynamic>.from(w as Map),
    ];
    final reference = '${mergeImportResult['reference'] ?? ''}';
    const actionText = <String, String>{
      'replaced': '替换',
      'inserted': '插入',
      'unchanged': '未动',
      'removed': '移除',
      'merged': '合并',
    };
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      color: Colors.teal.shade50,
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.call_merge, size: 18),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    (newWeapons.isEmpty && modified.isEmpty)
                        ? '合并导入 · 包里没有武器'
                        : '合并导入 · 新增 ${newWeapons.length} 把'
                            '${modified.isEmpty ? '' : '、覆盖已有 ${modified.length} 把'}',
                    style: const TextStyle(fontWeight: FontWeight.bold),
                  ),
                ),
                Text(
                  '${entries.length} 个条目 · ${mergeImportResult['assets']} 个素材',
                  style: const TextStyle(fontSize: 12, color: Colors.black54),
                ),
                IconButton(
                  tooltip: '关闭这张导入结果',
                  visualDensity: VisualDensity.compact,
                  icon: const Icon(Icons.close, size: 18),
                  onPressed: () => setState(() => mergeImportResult = {}),
                ),
              ],
            ),
            if (newWeapons.isNotEmpty) ...[
              const SizedBox(height: 6),
              const Text(
                '新增武器：',
                style: TextStyle(fontSize: 12, color: Colors.green),
              ),
              const SizedBox(height: 2),
              for (final w in newWeapons)
                Padding(
                  padding: const EdgeInsets.only(left: 8, bottom: 1),
                  child: Text(
                    '  · ${w['name']}（${w['id']}）',
                    style: const TextStyle(fontSize: 11, color: Colors.green),
                  ),
                ),
            ],
            if (modified.isNotEmpty) ...[
              const SizedBox(height: 6),
              const Text(
                '覆盖的已有武器（这些编号在目标客户端已存在，按合并包覆盖）：',
                style: TextStyle(fontSize: 12, color: Colors.deepOrange),
              ),
              const SizedBox(height: 2),
              for (final w in modified)
                Padding(
                  padding: const EdgeInsets.only(left: 8, bottom: 1),
                  child: Text(
                    '  · ${w['name']}（${w['id']}）',
                    style: const TextStyle(
                      fontSize: 11,
                      color: Colors.deepOrange,
                    ),
                  ),
                ),
            ],
            const SizedBox(height: 6),
            SelectableText(
              '改动前备份：${mergeImportResult['backup']}',
              style: const TextStyle(fontSize: 11, color: Colors.black54),
            ),
            if (reference.isNotEmpty) ...[
              const SizedBox(height: 2),
              Text(
                '参照配置：$reference',
                style: const TextStyle(fontSize: 11, color: Colors.black54),
              ),
            ],
            if (entries.isNotEmpty) ...[
              const SizedBox(height: 6),
              const Text('处理结果：', style: TextStyle(fontSize: 12)),
              const SizedBox(height: 2),
              for (final e in entries)
                Padding(
                  padding: const EdgeInsets.only(left: 8, bottom: 1),
                  child: Text(
                    '  · ${e['entry']} — ${actionText['${e['action']}'] ?? e['action']}'
                    '${e['detail'] == null ? '' : '（${e['detail']}）'}',
                    style: const TextStyle(fontSize: 11, color: Colors.black54),
                  ),
                ),
            ],
          ],
        ),
      ),
    );
  }

  Future<void> createWeapon() async {
    if (!(await discard()) || !mounted) return;
    final blueprint = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _BlueprintDialog(
        types: [for (final t in (data?['types'] as List? ?? [])) t],
        models: [for (final m in (data?['models'] as List? ?? [])) '$m'],
        donors: donors,
        minID: data?['blueprint_min'] as int? ?? 253000,
        maxID: data?['blueprint_max'] as int? ?? 253999,
        suggestedID: suggestID(),
        usedIDs: {
          for (final id in usedIDs) '$id',
        },
        onUploadIcon: (sourcePath) async {
          final r = await widget.api({
            'operation': 'weapon_icon_upload',
            'source_path': sourcePath,
          });
          return '${r['icon'] ?? ''}';
        },
      ),
    );
    if (blueprint == null || !mounted) return;
    setState(() {
      busy = true;
      failed = false;
      message = '正在登记自建武器…';
    });
    try {
      final result = await widget.api({
        'operation': 'weapon_create',
        'blueprint': blueprint,
      });
      if (!mounted) return;
      setState(() {
        dirty = false;
        message = '${result['message']}';
      });
      await load(prefer: blueprint['id'] as int);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  Future<void> forget() async {
    if (!(await discard()) || !mounted) return;
    final id = weapon!['id'];
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('移除自建武器'),
        content: Text(
          '将从编辑清单中移除「${weapon!['name']}」（$id）及其已保存的效果方案。\n\n'
          '已写入客户端的配置不会自动回滚：请再点一次「应用到游戏」，自建武器才会从配置包中消失。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('移除'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    setState(() {
      busy = true;
      failed = false;
      message = '正在移除自建武器…';
    });
    try {
      final result = await widget.api({
        'operation': 'weapon_forget',
        'weapon': id,
      });
      if (!mounted) return;
      setState(() {
        dirty = false;
        message = '${result['message']}';
      });
      await load(prefer: 253013);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  /// Edits the identity fields (name / icon / description) of a registered
  /// self-made weapon. The change only lands in settings.json; the next
  /// 「应用到游戏」 rebuilds the client rows from the updated blueprint.
  Future<void> editBlueprintInfo() async {
    if (!(await discard()) || !mounted) return;
    final id = weapon!['id'];
    final updated = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _BlueprintInfoDialog(
        initial: blueprintOf(id),
        onUploadIcon: (sourcePath) async {
          final r = await widget.api({
            'operation': 'weapon_icon_upload',
            'source_path': sourcePath,
          });
          return '${r['icon'] ?? ''}';
        },
      ),
    );
    if (updated == null || !mounted) return;
    setState(() {
      busy = true;
      failed = false;
      message = '正在更新武器信息…';
    });
    try {
      final result = await widget.api({
        'operation': 'weapon_blueprint_update',
        'weapon': id,
        'blueprint': updated,
      });
      if (!mounted) return;
      setState(() {
        dirty = false;
        message = '${result['message']}';
      });
      await load(prefer: id);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  /// Registers (or clears) a combo-table completion for the current weapon.
  /// A donor of 0 clears it.
  Future<void> setCombo(int donor) async {
    if (!(await discard()) || !mounted) return;
    final id = weapon!['id'];
    setState(() {
      busy = true;
      failed = false;
      message = donor == 0 ? '正在取消连招补齐…' : '正在登记连招补齐…';
    });
    try {
      final result = await widget.api({
        'operation': 'weapon_combo',
        'weapon': id,
        'donor': donor,
      });
      if (!mounted) return;
      setState(() {
        dirty = false;
        message = '${result['message']}';
      });
      await load(prefer: id);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  Future<void> pickComboDonor() async {
    if (!(await discard()) || !mounted) return;
    final id = weapon!['id'];
    final suggested = weapon!['combo_suggestion'] as int? ?? 0;
    final donor = await showDialog<int>(
      context: context,
      builder: (_) => _ComboDonorDialog(
        candidates: [
          for (final w in (data?['weapons'] as List? ?? []))
            if ((w['combo_rows'] as int? ?? 0) > 0 && w['id'] != id)
              Map<String, dynamic>.from(w as Map),
        ],
        suggested: suggested,
      ),
    );
    if (donor == null || !mounted) return;
    await setCombo(donor);
  }

  String buffName(dynamic id) {
    final value = int.tryParse('$id') ?? 0;
    if (value == 0) return '无';
    for (final b in (data?['buffs'] as List? ?? [])) {
      if (b['id'] == value) return '${b['name']}';
    }
    return '异常状态 $value（名称未收录）';
  }

  String reactionChoice(dynamic hit, Map<String, dynamic> rule) {
    final original = Map<String, dynamic>.from(hit['values']);
    final current = {...original, ...?rule['properties']?[hit['id']] as Map?};
    final fields = (data?['fields'] as List? ?? []).where(
      (f) => f['key'] != 'SkillDamage' && f['key'] != 'SkillEnhanceDamage',
    );
    if (fields.every((f) => '${current[f['key']]}' == '${original[f['key']]}')) {
      return 'original';
    }
    for (final e in (data?['effects'] as List? ?? [])) {
      if ((e['values'] as Map).entries.every(
        (v) => '${current[v.key]}' == '${v.value}',
      )) {
        return e['id'];
      }
    }
    return 'custom';
  }

  String originalReaction(dynamic hit) {
    final v = hit['values'];
    if ('${v['TripTarget']}' == '1') return '击倒';
    if ('${v['TargetFlurr']}' == '1' && '${v['StandHurtFly']}' == '11') {
      return '上升 / 悬浮';
    }
    if ('${v['RepulseTarget']}' == '1') return '击退';
    return '原受击动作';
  }

  String originalDebuff(dynamic stage) {
    final names = <String>{
      for (final h in (stage['hits'] as List? ?? [])) buffName(h['buff']),
    };
    return names.isEmpty ? '无' : names.join(' / ');
  }

  /// DEBUFF 下拉的一项：状态名字 + ustate.xml 注释里那句效果说明。
  ///
  /// 只显示名字时看不出副作用 —— type=27「恐惧」的注释是
  /// 「被动状态，level值无效 操作键都乱掉」，选了它进游戏就表现为技能放不出来。
  Widget buffChoice(Map<dynamic, dynamic> buff, dynamic stage) {
    final id = buff['id'];
    final desc = '${buff['desc'] ?? ''}';
    final title = id == 0 ? '默认（${originalDebuff(stage)}）' : '${buff['name']}';
    final risky = desc.contains('操作键') ||
        desc.contains('乱掉') ||
        desc.contains('无效') ||
        desc.contains('被动状态');
    if (desc.isEmpty) {
      return Text(title, overflow: TextOverflow.ellipsis);
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(title, overflow: TextOverflow.ellipsis),
        Text(
          risky ? '$desc　⚠ 可能影响操作' : desc,
          style: TextStyle(
            fontSize: 11,
            color: risky ? Colors.deepOrange : Colors.black54,
          ),
          overflow: TextOverflow.ellipsis,
        ),
      ],
    );
  }

  /// 「高级设置」下拉的一项：中文名 + 该档位的实际后果。
  ///
  /// 这些字段以前是裸数字框，作者只能猜 11 是什么意思。中文名与说明由后端
  /// hit_options 给出（取自官方 skillproperty.xml 实际用过的档位，配合
  /// animation/300501.xml 的策划注释反查），所以下拉里永远不会出现客户端
  /// 没有对应动画的数字。
  Widget hitOptionChoice(Map<dynamic, dynamic> option) {
    final value = option['value'];
    final label = '${option['label'] ?? ''}';
    final detail = '${option['detail'] ?? ''}';
    final risky = label.contains('勿用') ||
        label.contains('错配') ||
        label.contains('无受击') ||
        label.contains('无动作') ||
        label.contains('无倒地') ||
        label.contains('无落地') ||
        label.contains('不播放');
    if (detail.isEmpty) {
      return Text('$value　$label', overflow: TextOverflow.ellipsis);
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Text('$value　$label', overflow: TextOverflow.ellipsis),
        Text(
          risky ? '$detail　⚠ 慎用' : detail,
          style: TextStyle(
            fontSize: 11,
            color: risky ? Colors.deepOrange : Colors.black54,
          ),
          overflow: TextOverflow.ellipsis,
        ),
      ],
    );
  }

  /// 客户端在用、但选项表没收录的值。不能悄悄丢掉，否则一打开这个招式
  /// 下拉框就会把原值改掉。
  List<Map<String, dynamic>> extraHitOptions(
    dynamic field,
    int current,
  ) {
    final known = (data?['hit_options']?[field['key']] as List? ?? [])
        .map((o) => o['value'] as int)
        .toSet();
    if (known.contains(current)) return const [];
    return [
      {
        'value': current,
        'label': '客户端原生值（未收录音译）',
        'detail': '这个编号在官方配置里用到了，但暂无中文说明，保留原值更安全',
      },
    ];
  }

  Widget hitOptionEditor(
    dynamic hit,
    Map<String, dynamic> rule,
    bool enabled,
    dynamic field,
    int current,
  ) {
    final options = [
      ...(data?['hit_options']?[field['key']] as List? ?? []),
      ...extraHitOptions(field, current),
    ];
    return SizedBox(
      width: 300,
      child: DropdownButtonFormField<int>(
        key: ValueKey(
          '$editorVersion-${rule['stage']}-${hit['id']}-${field['key']}-$current',
        ),
        isExpanded: true,
        initialValue: current,
        decoration: InputDecoration(
          labelText: '${field['name']}（${field['key']}）',
          helperText: '默认 ${hit['values'][field['key']]}',
          helperMaxLines: 2,
        ),
        items: [
          for (final option in options)
            DropdownMenuItem<int>(
              value: option['value'] as int,
              child: hitOptionChoice(option as Map<dynamic, dynamic>),
            ),
        ],
        onChanged: !enabled
            ? null
            : (value) => setState(() {
                final changes = rule.putIfAbsent(
                  'properties',
                  () => <String, dynamic>{},
                ) as Map;
                final values = changes.putIfAbsent(
                  hit['id'],
                  () => <String, dynamic>{},
                ) as Map;
                values[field['key']] = value;
                editorVersion++;
                dirty = true;
              }),
      ),
    );
  }

  Widget numberEditors(
    dynamic hit,
    Map<String, dynamic> rule,
    bool enabled, {
    required bool damage,
  }) => Padding(
    padding: const EdgeInsets.all(8),
    child: Wrap(
      spacing: 12,
      runSpacing: 12,
      children: [
        for (final field in (data?['fields'] as List? ?? []).where(
          (f) =>
              damage ==
              ['SkillDamage', 'SkillEnhanceDamage'].contains(f['key']),
        ))
          if (!damage && field['oneshot'] == true)
            hitOptionEditor(
              hit,
              rule,
              enabled,
              field,
              int.tryParse(
                    '${rule['properties']?[hit['id']]?[field['key']] ?? hit['values'][field['key']]}',
                  ) ??
                  0,
            )
          else
            SizedBox(
              width: 170,
              child: TextFormField(
                key: ValueKey(
                  '$editorVersion-${rule['stage']}-${hit['id']}-${field['key']}',
                ),
                initialValue:
                    '${rule['properties']?[hit['id']]?[field['key']] ?? hit['values'][field['key']]}',
                enabled: enabled,
                decoration: InputDecoration(
                  labelText: field['name'],
                  helperText: '默认 ${hit['values'][field['key']]}',
                ),
                keyboardType: TextInputType.number,
                validator: (text) {
                  final v = num.tryParse(text ?? '');
                  return v == null ||
                          !v.isFinite ||
                          v < field['min'] ||
                          v > field['max'] ||
                          (!damage &&
                              (v != v.roundToDouble() ||
                                  ((weapon?['allowed_values']?[field['key']]
                                              as List?)
                                          ?.contains(v.toInt()) ==
                                      false)))
                      ? '数值超出范围'
                      : null;
                },
                onChanged: (text) => setState(() {
                  final changes = rule.putIfAbsent(
                    'properties',
                    () => <String, dynamic>{},
                  ) as Map;
                  final values = changes.putIfAbsent(
                    hit['id'],
                    () => <String, dynamic>{},
                  ) as Map;
                  values[field['key']] = num.tryParse(text) ?? -1;
                  dirty = true;
                }),
              ),
            ),
      ],
    ),
  );

  List<Widget> hitEditors(
    dynamic stage,
    Map<String, dynamic> rule,
    bool enabled,
  ) {
    return [
      for (final hit in (stage['hits'] as List? ?? []))
        Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if ((stage['hits'] as List).length > 1)
              Text('命中 ${(stage['hits'] as List).indexOf(hit) + 1}'),
            Padding(
              padding: const EdgeInsets.all(8),
              child: DropdownButtonFormField<String>(
                key: ValueKey(
                  '$editorVersion-${rule['stage']}-${hit['id']}-${reactionChoice(hit, rule)}-effect',
                ),
                isExpanded: true,
                initialValue: reactionChoice(hit, rule),
                decoration: const InputDecoration(labelText: '受击动作'),
                items: [
                  DropdownMenuItem(
                    value: 'original',
                    child: Text('默认（${originalReaction(hit)}）'),
                  ),
                  if (reactionChoice(hit, rule) == 'custom')
                    const DropdownMenuItem(
                      value: 'custom',
                      enabled: false,
                      child: Text('自定义受击动作'),
                    ),
                  for (final effect in (data?['effects'] as List? ?? []))
                    DropdownMenuItem(
                      value: effect['id'] as String,
                      child: Text(
                        effect['id'] == 'float' ? '上升 / 悬浮' : effect['name'],
                      ),
                    ),
                ],
                onChanged: !enabled
                    ? null
                    : (value) => setState(() {
                        final changes = rule.putIfAbsent(
                          'properties',
                          () => <String, dynamic>{},
                        ) as Map;
                        final values = changes.putIfAbsent(
                          hit['id'],
                          () => <String, dynamic>{},
                        ) as Map;
                        if (value == 'original') {
                          values.removeWhere(
                            (key, _) =>
                                key != 'SkillDamage' &&
                                key != 'SkillEnhanceDamage',
                          );
                          if (values.isEmpty) changes.remove(hit['id']);
                          if (changes.isEmpty) rule.remove('properties');
                        } else {
                          final effect = (data!['effects'] as List).firstWhere(
                            (e) => e['id'] == value,
                          );
                          values.addAll(effect['values'] as Map);
                        }
                        editorVersion++;
                        dirty = true;
                      }),
              ),
            ),
            numberEditors(hit, rule, enabled, damage: true),
            ExpansionTile(
              title: const Text('击飞参数 / 高级设置'),
              children: [numberEditors(hit, rule, enabled, damage: false)],
            ),
          ],
        ),
    ];
  }

  Map<String, dynamic> get remaps =>
      Map<String, dynamic>.from(data?['remaps'] as Map? ?? const {});

  Map<String, dynamic> remapFor(String state) {
    final per = remaps['${weapon!['id']}'];
    final value = per is Map ? per[state] : null;
    return value is Map ? Map<String, dynamic>.from(value) : const {};
  }

  /// 复用模板：把另一把武器某个状态的动作与命中属性复制到本段。
  Future<void> remapFromTemplate(String stateKey) async {
    final picked = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _RemapTemplateDialog(
        weapons: [
          for (final w in (data?['weapons'] as List? ?? []))
            Map<String, dynamic>.from(w as Map),
        ],
        self: weapon!['id'] as int,
      ),
    );
    if (picked == null || !mounted) return;
    await _callRemap(
      stateKey,
      params: {
        'operation': 'weapon_remap',
        'weapon': weapon!['id'],
        'stage': int.parse(stateKey),
        'template_weapon': picked['weapon'],
        'template_stage': picked['state'],
      },
      busyText: '正在复用动作与命中属性…',
    );
  }

  /// 新增命中属性节点：克隆一个模板节点到全新编号，再指定给本段。
  Future<void> addPropertyFor(String stateKey) async {
    Map<String, dynamic> catalog = {};
    try {
      catalog = Map<String, dynamic>.from(
        await widget.api({'operation': 'weapon_remap_options'}),
      );
    } catch (_) {}
    final properties = [
      for (final p in (catalog['properties'] as List? ?? []))
        Map<String, dynamic>.from(p as Map),
    ];
    final template = await showDialog<String>(
      context: context,
      builder: (_) => _PropertyPickerDialog(properties: properties),
    );
    if (template == null || !mounted) return;
    final prefer = weapon!['id'] as int?;
    setState(() {
      busy = true;
      failed = false;
      message = '正在新增命中属性节点…';
    });
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({'operation': 'weapon_property_add', 'template': template}),
      );
      final newId = '${result['property_id'] ?? ''}';
      if (!mounted) return;
      if (newId.isEmpty) {
        setState(() {
          busy = false;
          failed = true;
          message = '${result['message'] ?? '新增失败'}';
        });
        return;
      }
      final remap = Map<String, dynamic>.from(await widget.api({
        'operation': 'weapon_remap',
        'weapon': weapon!['id'],
        'stage': int.parse(stateKey),
        'property_id': newId,
      }));
      if (!mounted) return;
      setState(() {
        busy = false;
        message = '${remap['message'] ?? '已指定命中属性'}';
      });
      await load(prefer: prefer);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  Future<void> clearRemap(String stateKey) async {
    await _callRemap(
      stateKey,
      params: {
        'operation': 'weapon_remap',
        'weapon': weapon!['id'],
        'stage': int.parse(stateKey),
      },
      busyText: '正在取消重映射…',
    );
  }

  /// 自建武器尚未使用的状态列：定义新状态时可选。
  List<String> get unusedStates {
    final states = [for (final s in (data?['states'] as List? ?? [])) '$s'];
    final used = {
      for (final stage in (weapon?['stages'] as List? ?? []))
        '${(stage as Map)['state']}',
    };
    return states.where((s) => !used.contains(s)).toList();
  }

  /// 「连招与命中效果」列表标题旁的「添加动作」入口：先选一个尚未使用的
  /// 状态列，然后走 defineState（同一套定义对话框）。与「状态定义」卡片
  /// 是同一个功能，只是离列表更近。
  Future<void> addAction() async {
    final states = unusedStates;
    if (states.isEmpty) {
      setState(() {
        failed = true;
        message = '所有状态列都已被使用，不能再添加动作了';
      });
      return;
    }
    final picked = await showDialog<String>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('添加动作'),
        content: SizedBox(
          width: 420,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text('选择一个尚未使用的状态列：'),
              const SizedBox(height: 8),
              Flexible(
                child: SingleChildScrollView(
                  child: Column(
                    children: [
                      for (final s in states)
                        ListTile(
                          dense: true,
                          title: Text(s),
                          onTap: () => Navigator.pop(dialogContext, s),
                        ),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(dialogContext),
            child: const Text('取消'),
          ),
        ],
      ),
    );
    if (picked == null || !mounted) return;
    setState(() => addStatePick = picked);
    await defineState();
  }

  /// 为自建武器定义一个新状态：选一个尚未使用的状态列，再填动作 / 命中属性 /
  /// 动作说明（可从其它武器复用）。提交后该状态出现在招式列表里。
  Future<void> defineState() async {
    final stateKey = addStatePick;
    if (stateKey == null) {
      setState(() {
        failed = true;
        message = '请先选择要定义的状态列';
      });
      return;
    }
    await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text('定义状态 $stateKey'),
        content: SizedBox(
          width: 540,
          child: SingleChildScrollView(
            child: _RemapEditor(
              stateKey: stateKey,
              weaponId: weapon!['id'] as int,
              initial: const {},
              busy: busy,
              api: widget.api,
              weapons: [
                for (final w in (data?['weapons'] as List? ?? []))
                  Map<String, dynamic>.from(w as Map),
              ],
              onSaved: () async {
                Navigator.of(dialogContext).pop(true);
              },
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: const Text('关闭'),
          ),
        ],
      ),
    );
    if (!mounted) return;
    setState(() => addStatePick = null);
    await load(prefer: weapon!['id']);
  }

  /// 删除自建武器的一个状态：清零该动作列，移除重映射与已保存的效果编辑，
  /// 并清掉连招链中涉及该状态的转移。
  Future<void> clearState(String stateKey) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('删除状态 $stateKey'),
        content: const Text(
          '该状态的动作列会被清零，已保存的该段效果、以及连招链里涉及这个状态的转移都会一并移除。'
          '之后可以重新「定义状态」把它加回来。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('删除状态'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    final prefer = weapon!['id'] as int?;
    setState(() {
      busy = true;
      failed = false;
      message = '正在删除状态…';
    });
    try {
      final result = Map<String, dynamic>.from(await widget.api({
        'operation': 'weapon_state_clear',
        'weapon': weapon!['id'],
        'stage': int.parse(stateKey),
      }));
      if (!mounted) return;
      setState(() {
        busy = false;
        message = '${result['message'] ?? '已删除状态'}';
      });
      await load(prefer: prefer);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  /// 自建武器的状态管理卡片：新增状态列（从零定义）与提示。
  Widget stateBuilderCard() {
    if (weapon == null || !isCreated(weapon!['id'])) {
      return const SizedBox.shrink();
    }
    final states = unusedStates;
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.addchart_outlined, size: 18),
                const SizedBox(width: 8),
                const Text(
                  '状态定义（自建武器）',
                  style: TextStyle(fontWeight: FontWeight.bold),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    '每个状态指定动作、命中属性和动作说明；动作与命中属性都能从其它武器拉取',
                    style: TextStyle(fontSize: 11, color: Colors.black54),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Row(
              children: [
                SizedBox(
                  width: 200,
                  child: DropdownButtonFormField<String>(
                    key: ValueKey('add-state-$editorVersion'),
                    initialValue: addStatePick,
                    isExpanded: true,
                    decoration: const InputDecoration(
                      labelText: '新增状态列',
                      isDense: true,
                    ),
                    items: [
                      for (final s in states)
                        DropdownMenuItem(value: s, child: Text(s)),
                    ],
                    onChanged: busy
                        ? null
                        : (v) => setState(() => addStatePick = v),
                  ),
                ),
                const SizedBox(width: 10),
                FilledButton.icon(
                  onPressed: busy || addStatePick == null ? null : defineState,
                  icon: const Icon(Icons.add, size: 16),
                  label: const Text('定义状态'),
                ),
                const SizedBox(width: 10),
                if (states.isEmpty)
                  const Expanded(
                    child: Text(
                      '所有状态列都已被使用',
                      style: TextStyle(fontSize: 11, color: Colors.black54),
                    ),
                  ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _callRemap(
    String stateKey, {
    required Map<String, dynamic> params,
    required String busyText,
  }) async {
    final prefer = weapon!['id'] as int?;
    setState(() {
      busy = true;
      failed = false;
      message = busyText;
    });
    try {
      final result = Map<String, dynamic>.from(await widget.api(params));
      if (!mounted) return;
      setState(() {
        busy = false;
        message = '${result['message'] ?? '已更新'}';
      });
      await load(prefer: prefer);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  /// 重映射失效告警：某个状态的映射指向了不存在的动作或命中属性，此时招式
  /// 列表退回未映射前的结构，需要先修好或取消该映射。
  Widget remapErrorBanner() {
    final detail = '${data?['remap_error'] ?? ''}';
    if (detail.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Container(
        width: double.infinity,
        padding: const EdgeInsets.all(10),
        decoration: BoxDecoration(
          color: Colors.orange.shade50,
          borderRadius: BorderRadius.circular(6),
          border: Border.all(color: Colors.deepOrange.shade200),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.error_outline,
                    size: 16, color: Colors.deepOrange.shade700),
                const SizedBox(width: 6),
                Text(
                  '状态重映射暂不可用',
                  style: TextStyle(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                    color: Colors.deepOrange.shade800,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 4),
            SelectableText(
              '$detail\n招式列表已退回未映射前的结构；请修好该映射或取消它。',
              style: TextStyle(fontSize: 12, color: Colors.deepOrange.shade900),
            ),
          ],
        ),
      ),
    );
  }

  /// 结构重映射区块：可编辑的动作 / 命中属性 / 说明，复用模板把结果填进输入框。
  Widget remapSection(int index, Map<String, dynamic> stage) {
    final stateKey = '${stage['state']}';
    return _RemapEditor(
      key: ValueKey('remap-${weapon!['id']}-$stateKey'),
      stateKey: stateKey,
      weaponId: weapon!['id'] as int,
      initial: remapFor(stateKey),
      busy: busy,
      api: widget.api,
      weapons: [
        for (final w in (data?['weapons'] as List? ?? []))
          Map<String, dynamic>.from(w as Map),
      ],
      onSaved: () => load(prefer: weapon!['id']),
    );
  }


  Widget stageEditor(int index) {
    final rule = rules[index], stage = weapon!['stages'][index];
    final enabled = stage['supported'] == true && !busy;
    return Card(
      key: ValueKey("$editorVersion-${weapon!['id']}-${rule['stage']}"),
      child: Padding(
        padding: const EdgeInsets.all(14),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              stage['label'] ?? '动作说明缺失（按键待核实）',
              style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 17),
            ),
            const SizedBox(height: 2),
            Text(
              stageIdentity(index),
              style: const TextStyle(
                fontSize: 12,
                fontFamily: 'monospace',
                color: Colors.black54,
              ),
            ),
            if (stage['supported'] != true)
              Text(
                stage['reason'],
                style: const TextStyle(color: Colors.deepOrange),
              ),
            const SizedBox(height: 8),
            remapSection(index, Map<String, dynamic>.from(stage as Map)),
            if (isCreated(weapon!['id']))
              Align(
                alignment: Alignment.centerRight,
                child: TextButton.icon(
                  onPressed: busy
                      ? null
                      : () => clearState('${stage['state']}'),
                  icon: const Icon(Icons.delete_outline, size: 16),
                  label: Text('删除状态 ${stage['state']}'),
                ),
              ),
            const SizedBox(height: 12),
            Row(
              children: [
                Expanded(
                  flex: 3,
                  child: DropdownButtonFormField<int>(
                    isExpanded: true,
                    key: ValueKey(
                      '${weapon!['id']}-${rule['stage']}-buff-${rule['buff']}',
                    ),
                    initialValue: rule['buff'],
                    decoration: const InputDecoration(labelText: 'DEBUFF'),
                    // 收起状态只显示名字，展开菜单才带效果说明，免得输入框挤两行。
                    selectedItemBuilder: (_) => [
                      for (final b in (data!['buffs'] as List))
                        Align(
                          alignment: Alignment.centerLeft,
                          child: Text(
                            b['id'] == 0
                                ? '默认（${originalDebuff(stage)}）'
                                : '${b['name']}',
                            overflow: TextOverflow.ellipsis,
                          ),
                        ),
                    ],
                    items: (data!['buffs'] as List)
                        .map(
                          (b) => DropdownMenuItem<int>(
                            value: b['id'],
                            child: buffChoice(b, stage),
                          ),
                        )
                        .toList(),
                    onChanged: enabled
                        ? (v) => setState(() {
                            rule['buff'] = v;
                            dirty = true;
                          })
                        : null,
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: DropdownButtonFormField<int>(
                    isExpanded: true,
                    key: ValueKey(
                      '${weapon!['id']}-${rule['stage']}-level-${rule['level']}',
                    ),
                    initialValue: rule['level'],
                    decoration: const InputDecoration(labelText: '等级'),
                    items: [1, 2, 3]
                        .map(
                          (v) => DropdownMenuItem(value: v, child: Text('$v')),
                        )
                        .toList(),
                    onChanged: enabled && rule['buff'] != 0
                        ? (v) => setState(() {
                            rule['level'] = v;
                            dirty = true;
                          })
                        : null,
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  flex: 2,
                  child: TextFormField(
                    key: ValueKey('$editorVersion-${rule['stage']}-duration'),
                    initialValue: '${rule['duration']}',
                    enabled: enabled && rule['buff'] != 0,
                    decoration: const InputDecoration(labelText: '持续周期（原生值）'),
                    keyboardType: TextInputType.number,
                    validator: (v) {
                      final n = int.tryParse(v ?? '');
                      return n == null || n < 1 || n > 60000
                          ? '请输入 1–60000'
                          : null;
                    },
                    onChanged: (v) => setState(() {
                      rule['duration'] = int.tryParse(v) ?? 0;
                      dirty = true;
                    }),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            ...hitEditors(stage, rule, enabled),
          ],
        ),
      ),
    );
  }

  /// The 4-digit action state and the action (skill) id it resolves to. The
  /// state is what delayacttable.xml transitions on; the action id is what
  /// itemact.txt points at, and both are what you need when cross-checking the
  /// tables by hand.
  String stageIdentity(int? index) {
    if (index == null || index < 0 || index >= (weapon!['stages'] as List).length) {
      return '';
    }
    final stage = weapon!['stages'][index];
    final ids = (stage['property_ids'] as List? ?? []).join('、');
    return '状态 ${stage['state']} · 动作 ${stage['action']}'
        '${ids.isEmpty ? '' : ' · 命中属性 $ids'}';
  }

  Widget actionChoice(String key, int? index, String label) {
    // 自建武器的每条动作都可以直接删掉（= 删除该状态列），不用先点进编辑卡。
    final deletable = index != null && isCreated(weapon!['id']);
    return Column(
      children: [
        ListTile(
          title: Text(label),
          subtitle: index == null
              ? const Text('提示中的状态没有对应动作，不能编辑')
              : Text(
                  stageIdentity(index),
                  style: const TextStyle(fontSize: 12, fontFamily: 'monospace'),
                ),
          selected: selectedAction == key,
          trailing: index == null
              ? null
              : Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (deletable)
                      IconButton(
                        tooltip: '删除动作',
                        icon: const Icon(Icons.delete_outline, size: 18),
                        onPressed: busy
                            ? null
                            : () => clearState(
                                '${(weapon!['stages'][index!] as Map)['state']}'),
                      ),
                    const Icon(Icons.edit_outlined),
                  ],
                ),
          onTap: index == null || busy
              ? null
              : () {
                  if (!(form.currentState?.validate() ?? true)) return;
                  setState(() {
                    selectedAction = selectedAction == key ? null : key;
                  });
                },
        ),
        if (selectedAction == key && index != null) stageEditor(index),
      ],
    );
  }

  Widget weaponIcon(Map<dynamic, dynamic> value, double size) {
    final path = '${value['icon'] ?? ''}';
    final fallback = Icon(Icons.sports_martial_arts, size: size * .6);
    return SizedBox(
      width: size,
      height: size,
      child: path.isEmpty
          ? fallback
          : Image.file(
              File(path),
              fit: BoxFit.contain,
              errorBuilder: (_, error, stack) => fallback,
            ),
    );
  }

  /// Page-level strip: which client this editor works on and its full
  /// config.spf2 SHA-256. Only the current client's id is shown — comparing it
  /// against the server's stored value was noise.
  Widget clientStrip() {
    if (_clientInfo.isEmpty) return const SizedBox.shrink();
    final directory = '${_clientInfo['directory'] ?? ''}';
    final hash = clientConfigHash;
    final baselineState = '${(data?['client'] as Map?)?['state'] ?? ''}';
    final rebaseNeeded = baselineState == '有差异' ||
        baselineState == '未采集基线' ||
        baselineState == '客户端缺失';
    return Card(
      margin: const EdgeInsets.fromLTRB(12, 8, 12, 2),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 6, 8, 6),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.folder_open, size: 16),
                const SizedBox(width: 6),
                const Text(
                  '客户端',
                  style: TextStyle(fontSize: 12, fontWeight: FontWeight.bold),
                ),
                const SizedBox(width: 8),
                Flexible(
                  child: SelectableText(
                    directory,
                    maxLines: 1,
                    style: const TextStyle(fontSize: 12),
                  ),
                ),
                if (baselineState.isNotEmpty) ...[
                  const SizedBox(width: 8),
                  baselineChip(baselineState),
                ],
                if (rebaseNeeded) ...[
                  const SizedBox(width: 4),
                  Text(
                    baselineState == '客户端缺失'
                        ? '没有 Data/config.spf2，无法写入'
                        : '与手上的基线不一致',
                    style: TextStyle(
                      fontSize: 12,
                      color: Colors.deepOrange.shade800,
                    ),
                  ),
                  TextButton(
                    onPressed: busy ? null : rebaseClient,
                    child: const Text('重新采集基线',
                        style: TextStyle(fontSize: 12)),
                  ),
                ],
                const Spacer(),
                OutlinedButton.icon(
                  onPressed: busy ? null : chooseClient,
                  icon: const Icon(Icons.swap_horiz, size: 16),
                  label: const Text('更换客户端'),
                ),
              ],
            ),
            Row(
              children: [
                const SizedBox(width: 22),
                const Text(
                  'config.spf2 SHA-256',
                  style: TextStyle(fontSize: 11),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: SelectableText(
                    hash.isEmpty ? '(读不到)' : hash,
                    maxLines: 1,
                    style: const TextStyle(fontSize: 11, fontFamily: 'monospace'),
                  ),
                ),
                TextButton.icon(
                  onPressed: hash.isEmpty ? null : () => copyConfigHash(hash),
                  icon: const Icon(Icons.copy, size: 14),
                  label: const Text('复制', style: TextStyle(fontSize: 12)),
                ),
              ],
            ),
            if (undeployedIDs.isNotEmpty)
              Row(
                children: [
                  const SizedBox(width: 22),
                  Icon(
                    Icons.info_outline,
                    size: 14,
                    color: Colors.orange.shade800,
                  ),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      '编辑集里有 ${undeployedIDs.length} 把武器本客户端还没有'
                      '（列表里标「未部署」）；它们只是编辑集里的记录，'
                      '点「应用到游戏」或导入合并包之后才会真正写进这个客户端。',
                      style: TextStyle(
                        fontSize: 11,
                        color: Colors.orange.shade900,
                      ),
                    ),
                  ),
                ],
              ),
          ],
        ),
      ),
    );
  }

  Future<void> copyConfigHash(String hash) async {
    await Clipboard.setData(ClipboardData(text: hash));
    if (mounted) {
      setState(() => message = '已复制 config.spf2 的完整 SHA-256');
    }
  }

  Widget baselineChip(String state) {
    late final Color color;
    if (state == '已同步') {
      color = Colors.teal.shade700;
    } else if (state == '未写入') {
      color = Colors.blueGrey.shade600;
    } else {
      color = Colors.deepOrange.shade700;
    }
    return Padding(
      padding: const EdgeInsets.only(right: 4),
      child: Text(state, style: TextStyle(fontSize: 12, color: color)),
    );
  }

  /// Warns about weapons the client will refuse to chain.
  ///
  /// itemact.txt only names the animation for each state; the state machine
  /// that turns a key press into the next state lives in delayacttable.xml.
  /// Several shipped weapons (混沌宇宙, D眩晕之锤, 无名剑 …) copy another
  /// weapon's action row but were never registered there, so they look complete
  /// in this editor yet cannot combo in game. The fix is to borrow the
  /// transitions of the weapon whose action row they copied.
  Widget comboBanner() {
    final id = weapon!['id'];
    final rows = weapon!['combo_rows'] as int? ?? 0;
    final registered = comboDonorOf(id);
    if (rows > 0 && registered == null) return const SizedBox.shrink();

    final suggested = weapon!['combo_suggestion'] as int? ?? 0;
    final reference = registered ?? suggested;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Card(
        color: registered == null ? Colors.orange.shade50 : Colors.teal.shade50,
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Icon(
                    registered == null ? Icons.link_off : Icons.link,
                    size: 18,
                    color: registered == null
                        ? Colors.deepOrange.shade800
                        : Colors.teal.shade800,
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      registered == null
                          ? '缺少连招表：进游戏后只能出第一段，按键不会推进到下一段'
                          : '连招表已补齐：借用「${weaponName(reference)}」的 $rows 条转移',
                      style: TextStyle(
                        fontWeight: FontWeight.bold,
                        color: registered == null
                            ? Colors.deepOrange.shade900
                            : Colors.teal.shade900,
                      ),
                    ),
                  ),
                ],
              ),
              if (registered == null)
                Padding(
                  padding: const EdgeInsets.only(top: 6),
                  child: Text(
                    '动作行（itemact.txt）只决定每段放哪个动画，能否连到下一段由 delayacttable.xml 的状态机决定。'
                    '${suggested > 0 ? '这把武器的动作行与「${weaponName(suggested)}」一致，可以直接借用它的连招。' : ''}'
                    '补齐后需「应用到游戏」并重启客户端才生效。',
                    style: const TextStyle(fontSize: 12, height: 1.6),
                  ),
                ),
              const SizedBox(height: 6),
              Wrap(
                spacing: 10,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  if (registered == null && suggested > 0)
                    FilledButton.tonalIcon(
                      onPressed: busy ? null : () => setCombo(suggested),
                      icon: const Icon(Icons.auto_fix_high, size: 18),
                      label: Text('借用「${weaponName(suggested)}」的连招'),
                    ),
                  if (registered == null)
                    OutlinedButton.icon(
                      onPressed: busy ? null : pickComboDonor,
                      icon: const Icon(Icons.search, size: 18),
                      label: const Text('选择其它参考武器'),
                    ),
                  if (registered != null)
                    TextButton(
                      onPressed: busy ? null : () => setCombo(0),
                      child: const Text('取消连招补齐'),
                    ),
                  if (registered != null)
                    TextButton(
                      onPressed: busy ? null : pickComboDonor,
                      child: const Text('换一个参考武器'),
                    ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final allWeapons = data?['weapons'] as List? ?? [];
    final types =
        allWeapons.map((w) => '${w['type'] ?? '未分类'}').toSet().toList()..sort();
    final weapons = allWeapons
        .where(
          (w) =>
              '${w['name']} ${w['id']}'.contains(query.trim()) &&
              (weaponType == '全部类型' || '${w['type'] ?? '未分类'}' == weaponType),
        )
        .toList();
    final combos = weapon?['combos'] as List? ?? [];
    final stageIndices = <String, int>{
      for (var i = 0; i < rules.length; i++)
        '${weapon!['stages'][i]['state']}': i,
    };
    final mapped = {
      for (final c in combos)
        for (final n in c['nodes'] as List) '${n['state']}',
    };
    final otherStages = stageIndices.entries
        .where((e) => !mapped.contains(e.key))
        .map((e) => e.value)
        .toList();
    final applied = data?['applied']['${weapon?['id']}'] as List? ?? [];
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
        appBar: AppBar(
          title: const Text('武器配置'),
          actions: [
            TextButton.icon(
              onPressed: busy || data == null ? null : createWeapon,
              icon: const Icon(Icons.add),
              label: const Text('新建武器'),
            ),
            TextButton.icon(
              onPressed: busy
                  ? null
                  : () async {
                      if (await discard() && mounted) {
                        setState(() {
                          dirty = false;
                          busy = true;
                          message = '';
                        });
                        await load();
                      }
                    },
              icon: const Icon(Icons.refresh),
              label: const Text('重新读取'),
            ),
          ],
        ),
        body: Column(
          children: [
            if (busy) const LinearProgressIndicator(),
            clientStrip(),
            Expanded(
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  SizedBox(
                    width: 300,
                    child: Column(
                      children: [
                        Padding(
                          padding: const EdgeInsets.all(12),
                          child: TextField(
                            decoration: const InputDecoration(
                              labelText: '搜索武器名称 / 编号',
                              prefixIcon: Icon(Icons.search),
                            ),
                            onChanged: (value) => setState(() => query = value),
                          ),
                        ),
                        Padding(
                          padding: const EdgeInsets.symmetric(horizontal: 12),
                          child: DropdownButtonFormField<String>(
                            key: ValueKey('weapon-type-$weaponType'),
                            initialValue:
                                ['全部类型', ...types].contains(weaponType)
                                ? weaponType
                                : '全部类型',
                            decoration: const InputDecoration(
                              labelText: '武器类型',
                            ),
                            items: ['全部类型', ...types]
                                .map(
                                  (type) => DropdownMenuItem(
                                    value: type,
                                    child: Text(type),
                                  ),
                                )
                                .toList(),
                            onChanged: (value) =>
                                setState(() => weaponType = value ?? '全部类型'),
                          ),
                        ),
                        Padding(
                          padding: const EdgeInsets.all(8),
                          child: Text(deploySummary(allWeapons)),
                        ),
                        Expanded(
                          child: ListView.builder(
                            itemCount: weapons.length,
                            itemBuilder: (context, index) {
                              final value = weapons[index];
                              return ListTile(
                                leading: weaponIcon(value, 40),
                                selected: weapon?['id'] == value['id'],
                                title: Row(
                                  children: [
                                    Flexible(
                                      child: Text(
                                        '${value['name']}',
                                        overflow: TextOverflow.ellipsis,
                                      ),
                                    ),
                                    if (isCreated(value['id']))
                                      const Padding(
                                        padding: EdgeInsets.only(left: 6),
                                        child: _SelfMadeBadge(),
                                      ),
                                    if (!isDeployed(value['id']))
                                      const Padding(
                                        padding: EdgeInsets.only(left: 4),
                                        child: _NotDeployedBadge(),
                                      ),
                                  ],
                                ),
                                subtitle: Text(
                                  '${value['type'] ?? '未分类'} · ${value['id']}\n${(value['stages'] as List).length} 个招式',
                                ),
                                onTap: busy
                                    ? null
                                    : () async {
                                        if (value['id'] == weapon?['id']) {
                                          return;
                                        }
                                        if (await discard() && mounted) {
                                          setState(() {
                                            select(
                                              Map<String, dynamic>.from(value),
                                            );
                                            message = '';
                                          });
                                        }
                                      },
                              );
                            },
                          ),
                        ),
                      ],
                    ),
                  ),
                  const VerticalDivider(width: 1),
                  Expanded(
                    child: weapon == null
                        ? Center(child: Text(busy ? '读取武器动作配置…' : '没有可读取的武器'))
                        : Padding(
                            padding: const EdgeInsets.all(20),
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                SizedBox(
                                  height: 130,
                                  child: Card(
                                    child: Padding(
                                      padding: const EdgeInsets.all(12),
                                      child: Row(
                                        crossAxisAlignment:
                                            CrossAxisAlignment.start,
                                        children: [
                                          weaponIcon(weapon!, 72),
                                          const SizedBox(width: 16),
                                          Expanded(
                                            child: Column(
                                              crossAxisAlignment:
                                                  CrossAxisAlignment.start,
                                              children: [
                                                Text(
                                                  '${weapon!['name']}',
                                                  style: Theme.of(context)
                                                      .textTheme
                                                      .titleLarge,
                                                ),
                                                Text(
                                                  '${weapon!['type'] ?? '未分类'} · ${weapon!['id']}',
                                                ),
                                                if (isCreated(weapon!['id']))
                                                  Row(
                                                    children: [
                                                      const _SelfMadeBadge(),
                                                      const SizedBox(width: 8),
                                                      Expanded(
                                                        child: Text(
                                                          '复用模型 ${weapon!['model'] ?? '未知'} · ${donorSummary(weapon!['id'])}',
                                                          style: Theme.of(context)
                                                              .textTheme
                                                              .bodySmall,
                                                          overflow:
                                                              TextOverflow.ellipsis,
                                                        ),
                                                      ),
                                                    ],
                                                  ),
                                                if (!isDeployed(weapon!['id']))
                                                  Row(
                                                    children: [
                                                      const _NotDeployedBadge(),
                                                      const SizedBox(width: 8),
                                                      Expanded(
                                                        child: Text(
                                                          '本客户端的 config.spf2 里还没有这把武器，'
                                                          '它只是 GM 编辑集里的记录；'
                                                          '点「应用到游戏」才会写进这个客户端。',
                                                          style: TextStyle(
                                                            fontSize: 11,
                                                            color: Colors
                                                                .orange
                                                                .shade900,
                                                          ),
                                                        ),
                                                      ),
                                                    ],
                                                  ),
                                                const SizedBox(height: 6),
                                                const Text(
                                                  '武器简介',
                                                  style: TextStyle(
                                                    fontWeight: FontWeight.bold,
                                                  ),
                                                ),
                                                Expanded(
                                                  child: SingleChildScrollView(
                                                    key: ValueKey(
                                                      'weapon-description-${weapon!['id']}',
                                                    ),
                                                    child: SelectableText(
                                                      '${weapon!['description'] ?? ''}'
                                                              .trim()
                                                              .isEmpty
                                                          ? '暂无武器简介'
                                                          : '${weapon!['description']}',
                                                    ),
                                                  ),
                                                ),
                                              ],
                                            ),
                                          ),
                                        ],
                                      ),
                                    ),
                                  ),
                                ),
                                const SizedBox(height: 8),
                                comboBanner(),
                                remapErrorBanner(),
                                const SizedBox(height: 8),
                                // 左：连招/按键状态机；右：本武器的状态与伤害效果。
                                // 两块各自滚动，避免整页串成一条长列表。
                                Expanded(
                                  child: Row(
                                    crossAxisAlignment:
                                        CrossAxisAlignment.stretch,
                                    children: [
                                      SizedBox(
                                        width: 560,
                                        child: SingleChildScrollView(
                                          child: Column(
                                            crossAxisAlignment:
                                                CrossAxisAlignment.stretch,
                                            children: [
                                              comboChainCard(),
                                              frameSwitchCard(),
                                              comboRuleCard(),
                                            ],
                                          ),
                                        ),
                                      ),
                                      const VerticalDivider(width: 17),
                                      Expanded(
                                        child: Column(
                                          crossAxisAlignment:
                                              CrossAxisAlignment.start,
                                          children: [
                                            stateBuilderCard(),
                                            Row(
                                              children: [
                                                Expanded(
                                                  child: Text(
                                                    '连招与命中效果',
                                                    style: Theme.of(context)
                                                        .textTheme
                                                        .titleMedium,
                                                  ),
                                                ),
                                                if (isCreated(weapon!['id']))
                                                  TextButton.icon(
                                                    onPressed: busy
                                                        ? null
                                                        : addAction,
                                                    icon: const Icon(
                                                      Icons.add,
                                                      size: 16,
                                                    ),
                                                    label: const Text(
                                                      '添加动作',
                                                      style: TextStyle(
                                                        fontSize: 12,
                                                      ),
                                                    ),
                                                  ),
                                              ],
                                            ),
                                            const SizedBox(height: 4),
                                            Text(
                                              applied.isEmpty
                                                  ? '当前游戏配置：原效果'
                                                  : '当前游戏配置：已写入 ${applied.length} 段效果；需重启游戏加载',
                                              style: const TextStyle(
                                                fontSize: 12,
                                              ),
                                            ),
                                            const Text(
                                              '选择招式，设置 DEBUFF、受击动作和伤害。',
                                              style: TextStyle(fontSize: 12),
                                            ),
                                            if ((weapon!['combos'] as List? ?? [])
                                                .isEmpty)
                                              const Text(
                                                '未收录按键提示，按动作名称选择。',
                                                style:
                                                    TextStyle(fontSize: 12),
                                              ),
                                            if (weapon!['id'] == 253013)
                                              Align(
                                                alignment:
                                                    Alignment.centerLeft,
                                                child: TextButton.icon(
                                                  onPressed: busy
                                                      ? null
                                                      : () => setState(() {
                                                          editorVersion++;
                                                          for (final r in rules) {
                                                            r['buff'] =
                                                                r['stage'] == 1
                                                                ? 1
                                                                : r['stage'] == 2
                                                                ? 37
                                                                : 0;
                                                            r['level'] = 1;
                                                            r['duration'] =
                                                                3000;
                                                          }
                                                          dirty = true;
                                                        }),
                                                  icon: const Icon(
                                                    Icons.auto_fix_high,
                                                  ),
                                                  label: const Text(
                                                    '填入示例：第一下中毒，第二下燃烧',
                                                  ),
                                                ),
                                              ),
                                            const SizedBox(height: 6),
                                            Expanded(
                                              child: Form(
                                                key: form,
                                                child: ListView.builder(
                                                  itemCount:
                                                      combos.length +
                                                      (otherStages.isEmpty
                                                          ? 0
                                                          : 1),
                                                  itemBuilder: (context, group) {
                                                    final other =
                                                        group == combos.length;
                                                    final combo = other
                                                        ? null
                                                        : combos[group];
                                                    final nodes = other
                                                        ? <dynamic>[]
                                                        : combo['nodes']
                                                              as List;
                                                    final title = other
                                                        ? '动作说明（${otherStages.length}）'
                                                        : nodes.isEmpty
                                                        ? '按键提示不完整'
                                                        : '${nodes.last['keys']}';
                                                    return ExpansionTile(
                                                      initiallyExpanded:
                                                          other &&
                                                          combos.isEmpty,
                                                      key: ValueKey(
                                                        '${weapon!['id']}-combo-$group',
                                                      ),
                                                      title: Text(title),
                                                      subtitle: Text(
                                                        other
                                                            ? '按动作名称选择'
                                                            : '${combo['name']} · ${nodes.length} 个动作段',
                                                      ),
                                                      children: [
                                                        for (
                                                          var n = 0;
                                                          n <
                                                              (other
                                                                  ? otherStages
                                                                        .length
                                                                  : nodes
                                                                        .length);
                                                          n++
                                                        )
                                                          actionChoice(
                                                            '$group:$n',
                                                            other
                                                                ? otherStages[n]
                                                                : stageIndices['${nodes[n]['state']}'],
                                                            other
                                                                ? '${weapon!['stages'][otherStages[n]]['label'] ?? '动作说明缺失（按键待核实）'}'
                                                                : '第 ${n + 1} 段 · ${nodes[n]['keys']}',
                                                          ),
                                                      ],
                                                    );
                                                  },
                                                ),
                                              ),
                                            ),
                                          ],
                                        ),
                                      ),
                                    ],
                                  ),
                                ),
                                const SizedBox(height: 10),
                                Wrap(
                                  spacing: 12,
                                  runSpacing: 8,
                                  crossAxisAlignment: WrapCrossAlignment.center,
                                  children: [
                                    OutlinedButton.icon(
                                      onPressed: busy
                                          ? null
                                          : () => execute('weapon_save'),
                                      icon: const Icon(Icons.save_outlined),
                                      label: const Text('保存方案'),
                                    ),
                                    FilledButton.icon(
                                      onPressed: busy
                                          ? null
                                          : () => execute('weapon_apply'),
                                      icon: const Icon(Icons.check),
                                      label: const Text('应用到游戏'),
                                    ),
                                    TextButton(
                                      onPressed: busy || applied.isEmpty
                                          ? null
                                          : () => execute('weapon_restore'),
                                      child: const Text('恢复原效果'),
                                    ),
                                    if (isCreated(weapon!['id']))
                                      TextButton.icon(
                                        onPressed: busy
                                            ? null
                                            : editBlueprintInfo,
                                        icon: const Icon(
                                          Icons.edit_outlined,
                                          size: 18,
                                        ),
                                        label: const Text('编辑信息'),
                                      ),
                                    if (isCreated(weapon!['id']))
                                      TextButton(
                                        onPressed: busy ? null : forget,
                                        child: const Text('移除自建武器'),
                                      ),
                                    FilledButton.tonalIcon(
                                      onPressed: busy ? null : publish,
                                      icon: const Icon(
                                        Icons.cloud_upload_outlined,
                                      ),
                                      label: const Text('更新到线上'),
                                    ),
                                    OutlinedButton.icon(
                                      onPressed: busy ? null : exportPackage,
                                      icon: const Icon(
                                        Icons.inventory_2_outlined,
                                      ),
                                      label: const Text('导出发版包'),
                                    ),
                                    OutlinedButton.icon(
                                      onPressed: busy ? null : exportMergePackage,
                                      icon: const Icon(Icons.call_merge),
                                      label: const Text('导出合并包'),
                                    ),
                                    OutlinedButton.icon(
                                      onPressed: busy ? null : importMergePackage,
                                      icon: const Icon(Icons.file_upload_outlined),
                                      label: const Text('导入武器包'),
                                    ),
                                    if (dirty)
                                      const Text(
                                        '有未保存修改',
                                        style: TextStyle(
                                          color: Colors.deepOrange,
                                        ),
                                      ),
                                  ],
                                ),
                                if (mergeImportResult.isNotEmpty) ...[
                                  const SizedBox(height: 8),
                                  mergeImportCard(),
                                ],
                                if (exportResult.isNotEmpty) ...[
                                  const SizedBox(height: 8),
                                  exportCard(),
                                ],
                              ],
                            ),
                          ),
                  ),
                ],
              ),
            ),
            if (message.isNotEmpty)
              Container(
                width: double.infinity,
                padding: const EdgeInsets.fromLTRB(14, 6, 6, 6),
                color: failed ? Colors.red.shade50 : Colors.teal.shade50,
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(
                      child: Padding(
                        padding: const EdgeInsets.symmetric(vertical: 8),
                        child: SelectableText(
                          message,
                          style: TextStyle(
                            color: failed
                                ? Colors.red.shade900
                                : Colors.teal.shade900,
                          ),
                        ),
                      ),
                    ),
                    IconButton(
                      tooltip: '关闭提示',
                      visualDensity: VisualDensity.compact,
                      icon: Icon(
                        Icons.close,
                        size: 18,
                        color: failed
                            ? Colors.red.shade900
                            : Colors.teal.shade900,
                      ),
                      onPressed: () => setState(() {
                        message = '';
                        failed = false;
                      }),
                    ),
                  ],
                ),
              ),
          ],
        ),
      ),
    );
  }
}

/// 发版包导出选项：导出哪把（这把 / 全部自建）、带哪些素材、要不要带草稿。
class _ExportDialog extends StatefulWidget {
  const _ExportDialog({required this.weaponName, required this.createdCount});
  final String weaponName;
  final int createdCount;

  @override
  State<_ExportDialog> createState() => _ExportDialogState();
}

class _ExportDialogState extends State<_ExportDialog> {
  final selected = <String>{for (final k in packageSections.keys) k};
  bool all = false;
  bool appliedOnly = false;

  @override
  Widget build(BuildContext context) {
    final disabled = selected.isEmpty;
    return AlertDialog(
      title: const Text('导出发版包'),
      content: SizedBox(
        width: 560,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text(
                '把武器用到的配置包和素材打成一个 zip，包里的路径就是客户端根目录下的'
                '相对路径（Data/config.spf2、Data/Weapon/Model/...），运维拿到后'
                '整包解压、覆盖到客户端根目录即可。',
              ),
              const SizedBox(height: 12),
              // 单选/开关类控件在这个界面上容易被压变形，一律用复选框。
              CheckboxListTile(
                value: all,
                onChanged: widget.createdCount == 0
                    ? null
                    : (v) => setState(() => all = v == true),
                dense: true,
                contentPadding: EdgeInsets.zero,
                controlAffinity: ListTileControlAffinity.leading,
                title: Text('导出全部自建武器（${widget.createdCount} 把）'),
                subtitle: Text(
                  all
                      ? '包里带上每一把自建武器的素材'
                      : '不勾则只导出「${widget.weaponName}」',
                  style: const TextStyle(fontSize: 11),
                ),
              ),
              CheckboxListTile(
                value: appliedOnly,
                onChanged: (v) => setState(() => appliedOnly = v == true),
                dense: true,
                contentPadding: EdgeInsets.zero,
                controlAffinity: ListTileControlAffinity.leading,
                title: const Text('只包含已应用到客户端的方案'),
                subtitle: const Text(
                  '默认和「更新到线上」一致：草稿也进包。勾上则只发本机客户端里'
                  '已经验证过的那部分，未应用的编辑不带走。',
                  style: TextStyle(fontSize: 11),
                ),
              ),
              const SizedBox(height: 6),
              const Text('带上的内容：', style: TextStyle(fontSize: 12)),
              for (final entry in packageSections.entries)
                CheckboxListTile(
                  value: selected.contains(entry.key),
                  onChanged: (v) => setState(
                    () => v == true
                        ? selected.add(entry.key)
                        : selected.remove(entry.key),
                  ),
                  dense: true,
                  contentPadding: EdgeInsets.zero,
                  controlAffinity: ListTileControlAffinity.leading,
                  title: Text(entry.value, style: const TextStyle(fontSize: 13)),
                ),
              if (disabled)
                const Padding(
                  padding: EdgeInsets.only(top: 6),
                  child: Text(
                    '至少勾一项。',
                    style: TextStyle(fontSize: 12, color: Colors.deepOrange),
                  ),
                ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: disabled
              ? null
              : () => Navigator.pop(context, {
                  'all': all,
                  'applied_only': appliedOnly,
                  'include': [for (final k in packageSections.keys) if (selected.contains(k)) k],
                }),
          child: const Text('导出'),
        ),
      ],
    );
  }
}

/// 底层按键码 → 可读标签。与后端 frameKeyLabel 同一套编号（7=X 8=C 9=Z
/// 5=跳 20=前 21=后），逗号是连按序列，前导负号是松开。
String frameKeyName(String code) {
  var value = code.trim();
  var release = false;
  if (value.startsWith('-')) {
    release = true;
    value = value.substring(1);
  }
  const names = {
    '7': 'X',
    '8': 'C',
    '9': 'Z',
    '5': '跳',
    '20': '前',
    '21': '后',
  };
  final name = names[value] ?? '键$value';
  return release ? '松开$name' : name;
}

String frameKeyLabelOf(String keycode) {
  final trimmed = keycode.trim();
  if (trimmed.isEmpty) return '（自动）';
  return trimmed.split(',').map(frameKeyName).join(' ');
}

/// 帧级连招的编辑对话框：一个状态一条切换，字段与动作块里的
/// `<CustomStateSwitch>` 属性一一对应。
class _FrameSwitchDialog extends StatefulWidget {
  const _FrameSwitchDialog({
    required this.state,
    required this.states,
    required this.keys,
  });

  final String state;
  final List<String> states;
  final List<Map<String, String>> keys;

  @override
  State<_FrameSwitchDialog> createState() => _FrameSwitchDialogState();
}

class _FrameSwitchDialogState extends State<_FrameSwitchDialog> {
  final form = GlobalKey<FormState>();
  late String state;
  late String next;
  late String key;
  final custom = TextEditingController();
  final interval = TextEditingController(text: '10');
  final inputStart = TextEditingController();
  final inputEnd = TextEditingController();
  final switchStart = TextEditingController(text: '10');
  final switchEnd = TextEditingController(text: '20');
  bool customKey = false;

  @override
  void initState() {
    super.initState();
    state = widget.state;
    next = widget.states.contains(widget.state)
        ? widget.state
        : (widget.states.isNotEmpty ? widget.states.first : '');
    key = widget.keys.isNotEmpty ? '${widget.keys.first['v']}' : '';
  }

  @override
  void dispose() {
    custom.dispose();
    interval.dispose();
    inputStart.dispose();
    inputEnd.dispose();
    switchStart.dispose();
    switchEnd.dispose();
    super.dispose();
  }

  String? frameField(String? value, String label, {bool required = true}) {
    final text = (value ?? '').trim();
    if (text.isEmpty) return required ? '请填 $label' : null;
    final number = int.tryParse(text);
    if (number == null || number < 0 || number > 9999) {
      return '$label 需要 0..9999 的整数';
    }
    return null;
  }

  /// 空字符串 = 这条切换不看按键（合法）；null = 写法非法。
  String? keyCodeValue() {
    final text = (customKey ? custom.text : key).trim();
    if (text.isEmpty) return '';
    for (final part in text.split(',')) {
      final body = part.startsWith('-') ? part.substring(1) : part;
      if (body.isEmpty || int.tryParse(body) == null) return null;
    }
    return text;
  }

  void submit() {
    if (!(form.currentState?.validate() ?? false)) return;
    final codes = <String, String>{};
    final resolved = keyCodeValue();
    if (resolved != null && resolved.isNotEmpty) codes['keycode'] = resolved;
    final startIn = inputStart.text.trim();
    final endIn = inputEnd.text.trim();
    if (startIn.isNotEmpty && endIn.isNotEmpty) {
      codes['inputstartframe'] = startIn;
      codes['inputendframe'] = endIn;
    }
    final gap = interval.text.trim();
    if (gap.isNotEmpty) codes['keyintervalframe'] = gap;
    codes['switchstartframe'] = switchStart.text.trim();
    codes['switchendframe'] = switchEnd.text.trim();
    codes['nextstate'] = next;
    const order = [
      'inputstartframe',
      'inputendframe',
      'keycode',
      'keyintervalframe',
      'switchstartframe',
      'switchendframe',
      'nextstate',
    ];
    Navigator.pop(context, <String, dynamic>{
      'attrs': [
        for (final name in order)
          if (codes.containsKey(name)) {'key': name, 'value': codes[name]},
      ],
    });
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('添加帧级连招'),
      content: SizedBox(
        width: 560,
        child: Form(
          key: form,
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text(
                  '动作播放到「生效窗口」期间按下按键，就会跳到「目标状态」。'
                  '同一条动作块被别的武器共用时，保存时会自动克隆出这把武器'
                  '独占的块，原武器不受影响。',
                  style: TextStyle(fontSize: 12),
                ),
                const SizedBox(height: 10),
                DropdownButtonFormField<String>(
                  initialValue: widget.states.contains(state) ? state : null,
                  decoration: const InputDecoration(labelText: '所在状态'),
                  items: [
                    for (final s in widget.states)
                      DropdownMenuItem(value: s, child: Text(s)),
                  ],
                  onChanged: (value) => setState(() => state = value ?? state),
                ),
                const SizedBox(height: 8),
                DropdownButtonFormField<String>(
                  initialValue: customKey ? '__custom__' : key,
                  decoration: const InputDecoration(labelText: '按键'),
                  items: [
                    for (final k in widget.keys)
                      DropdownMenuItem(
                        value: '${k['v']}',
                        child: Text('${k['l']}（${k['v']}）'),
                      ),
                    const DropdownMenuItem(
                      value: '__custom__',
                      child: Text('自定义 / 连按序列…'),
                    ),
                  ],
                  onChanged: (value) => setState(() {
                    customKey = value == '__custom__';
                    if (!customKey && value != null) key = value;
                  }),
                ),
                if (customKey) ...[
                  const SizedBox(height: 8),
                  TextFormField(
                    controller: custom,
                    decoration: const InputDecoration(
                      labelText: '按键码',
                      hintText: '例如 7,8 表示连按 X 再按 C；-7 表示松开 X',
                    ),
                    validator: (value) =>
                        keyCodeValue() == null ? '按键码只能是数字，逗号分隔' : null,
                  ),
                ] else
                  TextFormField(
                    initialValue: key,
                    decoration: const InputDecoration(
                      labelText: '按键码（可改）',
                      hintText: '例如 8 = C',
                    ),
                    onChanged: (value) => key = value.trim(),
                    validator: (value) =>
                        keyCodeValue() == null ? '按键码只能是数字，逗号分隔' : null,
                  ),
                const SizedBox(height: 8),
                DropdownButtonFormField<String>(
                  initialValue: widget.states.contains(next) ? next : null,
                  decoration: const InputDecoration(labelText: '目标状态'),
                  items: [
                    for (final s in widget.states)
                      DropdownMenuItem(value: s, child: Text(s)),
                  ],
                  validator: (value) => value == null ? '请选择目标状态' : null,
                  onChanged: (value) => setState(() => next = value ?? next),
                ),
                const SizedBox(height: 8),
                Row(
                  children: [
                    Expanded(
                      child: TextFormField(
                        controller: switchStart,
                        decoration: const InputDecoration(
                          labelText: '生效窗口起（帧）',
                        ),
                        validator: (v) => frameField(v, '生效窗口起'),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: TextFormField(
                        controller: switchEnd,
                        decoration: const InputDecoration(
                          labelText: '生效窗口止（帧）',
                        ),
                        validator: (v) => frameField(v, '生效窗口止'),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 8),
                Row(
                  children: [
                    Expanded(
                      child: TextFormField(
                        controller: inputStart,
                        decoration: const InputDecoration(
                          labelText: '输入窗口起（可空）',
                        ),
                        validator: (v) =>
                            frameField(v, '输入窗口起', required: false),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: TextFormField(
                        controller: inputEnd,
                        decoration: const InputDecoration(
                          labelText: '输入窗口止（可空）',
                        ),
                        validator: (v) =>
                            frameField(v, '输入窗口止', required: false),
                      ),
                    ),
                    const SizedBox(width: 8),
                    SizedBox(
                      width: 130,
                      child: TextFormField(
                        controller: interval,
                        decoration: const InputDecoration(
                          labelText: '连按间隔（可空）',
                        ),
                        validator: (v) =>
                            frameField(v, '连按间隔', required: false),
                      ),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        FilledButton(onPressed: submit, child: const Text('添加')),
      ],
    );
  }
}

/// 通用单选对话框：下拉太长时的搜索式挑选。
class _SimplePickDialog extends StatefulWidget {
  const _SimplePickDialog({
    required this.title,
    required this.hint,
    required this.options,
  });

  final String title;
  final String hint;
  final List<Map<String, String>> options;

  @override
  State<_SimplePickDialog> createState() => _SimplePickDialogState();
}

class _SimplePickDialogState extends State<_SimplePickDialog> {
  String query = '';

  @override
  Widget build(BuildContext context) {
    final matches = [
      for (final option in widget.options)
        if ('${option['value']} ${option['label']}'.contains(query.trim()))
          option,
    ];
    return AlertDialog(
      title: Text(widget.title),
      content: SizedBox(
        width: 420,
        height: 380,
        child: Column(
          children: [
            TextField(
              decoration: InputDecoration(
                labelText: widget.hint,
                prefixIcon: const Icon(Icons.search),
              ),
              onChanged: (value) => setState(() => query = value),
            ),
            const SizedBox(height: 8),
            Expanded(
              child: ListView.builder(
                itemCount: matches.length,
                itemBuilder: (context, index) => ListTile(
                  dense: true,
                  title: Text(
                    '${matches[index]['label']}',
                    style: const TextStyle(fontSize: 12),
                  ),
                  onTap: () =>
                      Navigator.pop(context, '${matches[index]['value']}'),
                ),
              ),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
      ],
    );
  }
}

/// Small tag shown next to weapons that only exist in the editor state.
class _SelfMadeBadge extends StatelessWidget {
  const _SelfMadeBadge();

  @override
  Widget build(BuildContext context) => Container(
    padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
    decoration: BoxDecoration(
      color: Colors.teal.shade100,
      borderRadius: BorderRadius.circular(4),
    ),
    child: Text(
      '自建',
      style: TextStyle(fontSize: 11, color: Colors.teal.shade900),
    ),
  );
}

/// Tag for weapons the editing set provides but the selected client's own
/// config.spf2 has no row for: visible in the list, not actually installed.
class _NotDeployedBadge extends StatelessWidget {
  const _NotDeployedBadge();

  @override
  Widget build(BuildContext context) => Container(
    padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
    decoration: BoxDecoration(
      color: Colors.orange.shade100,
      borderRadius: BorderRadius.circular(4),
    ),
    child: Text(
      '未部署',
      style: TextStyle(fontSize: 11, color: Colors.orange.shade900),
    ),
  );
}

/// Collects the fields needed to invent a weapon that the untouched client can
/// still draw.
///
/// A self-made weapon is a new row in item.txt plus a clone of an existing row
/// in itemact.txt, so it must borrow a RenderWare clump the client already
/// ships (we only add rows, we never add archives). The donor supplies the
/// per-stage animation ids that get cloned and isolated server side.
class _BlueprintDialog extends StatefulWidget {
  const _BlueprintDialog({
    required this.types,
    required this.models,
    required this.donors,
    required this.minID,
    required this.maxID,
    required this.suggestedID,
    required this.usedIDs,
    required this.onUploadIcon,
  });

  final List types;
  final List<String> models;
  final List<Map<String, dynamic>> donors;
  final int minID, maxID, suggestedID;
  final Set<String> usedIDs;
  final Future<String?> Function(String sourcePath) onUploadIcon;

  @override
  State<_BlueprintDialog> createState() => _BlueprintDialogState();
}

class _BlueprintDialogState extends State<_BlueprintDialog> {
  final form = GlobalKey<FormState>();
  late final TextEditingController number;
  late final TextEditingController name;
  final note = TextEditingController();
  final icon = TextEditingController();
  final description = TextEditingController();
  String type = '';
  String? model;
  int? donor;
  String modelNotice = '';

  @override
  void initState() {
    super.initState();
    number = TextEditingController(text: '${widget.suggestedID}');
    name = TextEditingController();
    type = widget.types.isEmpty ? '1' : '${widget.types.first['value']}';
  }

  @override
  void dispose() {
    number.dispose();
    name.dispose();
    note.dispose();
    icon.dispose();
    description.dispose();
    super.dispose();
  }

  String typeValueFor(String label) {
    for (final value in widget.types) {
      if ('${value['label']}' == label) return '${value['value']}';
    }
    return '';
  }

  /// Reusing the donor's appearance is the whole point: an unknown model name
  /// would leave the client rendering nothing. Donor 0 means "no template": the
  /// author picks the model and every state by hand.
  void adoptDonor(int? value) {
    setState(() {
      donor = value;
      modelNotice = '';
      if (value == null || value == 0) return;
      final weapon = widget.donors.firstWhere((w) => w['id'] == value);
      final candidate = '${weapon['model'] ?? ''}';
      if (widget.models.contains(candidate)) {
        model = candidate;
      } else {
        // A handful of shipped weapons were re-skinned without keeping their
        // original clump, so their model column names a file the client no
        // longer has. Do not guess an appearance; ask the author to choose.
        modelNotice = '客户端没有 $candidate，请手动选择模型';
      }
      final mapped = typeValueFor('${weapon['type'] ?? ''}');
      if (mapped.isNotEmpty) type = mapped;
      if (name.text.trim().isEmpty) {
        name.text = '自建${weapon['name']}';
      }
    });
  }

  /// Lets the author pick a local PNG, upload it into the client's item-icon
  /// directory, and drop the resulting icon path into the icon field.
  Future<void> pickAndUploadIcon() async {
    final source = await showDialog<String>(
      context: context,
      builder: (context) => _IconUploadDialog(),
    );
    if (source == null || !mounted) return;
    try {
      final result = await widget.onUploadIcon(source);
      if (!mounted || result == null || result.isEmpty) return;
      icon.text = result;
      setState(() {});
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('$e')));
      }
    }
  }

  void submit() {
    if (!(form.currentState?.validate() ?? false)) return;
    Navigator.pop(context, <String, dynamic>{
      'id': int.tryParse(number.text.trim()) ?? 0,
      'name': name.text.trim(),
      'type': type,
      'model': model,
      'donor': donor,
      'icon': icon.text.trim(),
      'description': description.text.trim(),
      'note': note.text.trim(),
    });
  }

  @override
  Widget build(BuildContext context) {
    final sorted = [...widget.donors]
      ..sort((a, b) => (a['id'] as int).compareTo(b['id'] as int));
    final models = [...widget.models]..sort();
    return AlertDialog(
      title: const Text('新建武器（不改动客户端）'),
      content: SizedBox(
        width: 540,
        child: Form(
          key: form,
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text(
                  '自建武器会在配置包里新增两行数据，并复用客户端已有的模型与动作，因此无需改动客户端。'
                  '编号使用预留区间，不影响原有武器。',
                ),
                const SizedBox(height: 16),
                Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    SizedBox(
                      width: 170,
                      child: TextFormField(
                        controller: number,
                        decoration: InputDecoration(
                          labelText: '武器编号',
                          helperText: '${widget.minID}–${widget.maxID}',
                        ),
                        keyboardType: TextInputType.number,
                        validator: (text) {
                          final v = int.tryParse((text ?? '').trim());
                          if (v == null || v < widget.minID || v > widget.maxID) {
                            return '编号超出预留区间';
                          }
                          if (widget.usedIDs.contains('$v')) return '编号已被占用';
                          return null;
                        },
                      ),
                    ),
                    const SizedBox(width: 14),
                    Expanded(
                      child: TextFormField(
                        controller: name,
                        decoration: const InputDecoration(
                          labelText: '武器名称',
                          helperText: '1–24 字',
                        ),
                        validator: (text) {
                          final value = (text ?? '').trim();
                          if (value.isEmpty || value.runes.length > 24) {
                            return '名称需为 1–24 个字符';
                          }
                          if (value.contains('\t')) return '名称不能包含制表符';
                          return null;
                        },
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 14),
                DropdownButtonFormField<int>(
                  isExpanded: true,
                  menuMaxHeight: 340,
                  initialValue: donor,
                  decoration: const InputDecoration(
                    labelText: '供体武器（复制其招式结构）',
                    helperText: '选一件作为动作模板；选「无供体」则从零创建',
                  ),
                  items: [
                    const DropdownMenuItem<int>(
                      value: 0,
                      child: Text('无供体（从零创建，动作与命中属性全空）'),
                    ),
                    for (final weapon in sorted)
                      DropdownMenuItem<int>(
                        value: weapon['id'] as int,
                        child: Text(
                          '${weapon['name']} · ${weapon['id']} · ${weapon['type'] ?? '未分类'}',
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                  ],
                  onChanged: adoptDonor,
                  validator: (value) => value == null ? '请选择供体武器' : null,
                ),
                const SizedBox(height: 14),
                Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(
                      child: DropdownButtonFormField<String>(
                        isExpanded: true,
                        initialValue: type.isEmpty ? null : type,
                        decoration: const InputDecoration(labelText: '武器子类'),
                        items: [
                          for (final value in widget.types)
                            DropdownMenuItem<String>(
                              value: '${value['value']}',
                              child: Text('${value['label']}'),
                            ),
                        ],
                        onChanged: (value) =>
                            setState(() => type = value ?? type),
                        validator: (value) =>
                            value == null || value.isEmpty ? '请选择子类' : null,
                      ),
                    ),
                    const SizedBox(width: 14),
                    SizedBox(
                      width: 230,
                      child: DropdownButtonFormField<String>(
                        key: ValueKey('blueprint-model-$model'),
                        isExpanded: true,
                        menuMaxHeight: 340,
                        initialValue: model,
                        decoration: const InputDecoration(
                          labelText: '模型（.dff）',
                          helperText: '只能选客户已有的模型',
                        ),
                        items: [
                          for (final value in models)
                            DropdownMenuItem<String>(
                              value: value,
                              child: Text(
                                value,
                                overflow: TextOverflow.ellipsis,
                              ),
                            ),
                        ],
                        onChanged: (value) => setState(() => model = value),
                        validator: (value) =>
                            value == null || value.isEmpty ? '请选择模型' : null,
                      ),
                    ),
                  ],
                ),
                if (modelNotice.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(top: 8),
                    child: Text(
                      modelNotice,
                      style: const TextStyle(
                        color: Colors.deepOrange,
                        fontSize: 12,
                      ),
                    ),
                  ),
                const SizedBox(height: 14),
                Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(
                      child: TextFormField(
                        controller: icon,
                        decoration: const InputDecoration(
                          labelText: '图标（可选）',
                          helperText: '相对 Data/UI 的路径；留空则复用供体图标',
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Padding(
                      padding: const EdgeInsets.only(top: 6),
                      child: OutlinedButton.icon(
                        onPressed: pickAndUploadIcon,
                        icon: const Icon(Icons.upload_file, size: 16),
                        label: const Text('上传图片'),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 14),
                TextFormField(
                  controller: description,
                  maxLength: 200,
                  minLines: 1,
                  maxLines: 3,
                  decoration: const InputDecoration(
                    labelText: '武器简介（可选）',
                    helperText: '游戏内展示的武器说明；留空则显示武器名称',
                  ),
                ),
                const SizedBox(height: 14),
                TextFormField(
                  controller: note,
                  maxLength: 200,
                  decoration: const InputDecoration(
                    labelText: '备注（可选，仅本地记录）',
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        FilledButton(onPressed: submit, child: const Text('登记自建武器')),
      ],
    );
  }
}

/// Edits the identity fields of an already-registered self-made weapon.
///
/// Only name, icon, description and note are editable: id, donor, subtype and
/// model stay fixed because the action row was cloned from the donor. Leaving
/// the icon empty inherits the donor's picture again.
class _BlueprintInfoDialog extends StatefulWidget {
  const _BlueprintInfoDialog({
    required this.initial,
    required this.onUploadIcon,
  });

  final Map<String, dynamic> initial;
  final Future<String?> Function(String sourcePath) onUploadIcon;

  @override
  State<_BlueprintInfoDialog> createState() => _BlueprintInfoDialogState();
}

class _BlueprintInfoDialogState extends State<_BlueprintInfoDialog> {
  final form = GlobalKey<FormState>();
  late final TextEditingController name;
  late final TextEditingController icon;
  late final TextEditingController description;
  late final TextEditingController note;

  @override
  void initState() {
    super.initState();
    name = TextEditingController(text: '${widget.initial['name'] ?? ''}');
    icon = TextEditingController(text: '${widget.initial['icon'] ?? ''}');
    description = TextEditingController(
      // 旧蓝图没有简介字段，当时简介列就是武器名；编辑时也这样回填，
      // 免得用户一保存就把简介改没了。
      text: '${widget.initial['description'] ?? ''}',
    );
    note = TextEditingController(text: '${widget.initial['note'] ?? ''}');
  }

  @override
  void dispose() {
    name.dispose();
    icon.dispose();
    description.dispose();
    note.dispose();
    super.dispose();
  }

  Future<void> pickAndUploadIcon() async {
    final source = await showDialog<String>(
      context: context,
      builder: (context) => _IconUploadDialog(),
    );
    if (source == null || !mounted) return;
    try {
      final result = await widget.onUploadIcon(source);
      if (!mounted || result == null || result.isEmpty) return;
      icon.text = result;
      setState(() {});
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('$e')));
      }
    }
  }

  void submit() {
    if (!(form.currentState?.validate() ?? false)) return;
    Navigator.pop(context, <String, dynamic>{
      'name': name.text.trim(),
      'icon': icon.text.trim(),
      'description': description.text.trim(),
      'note': note.text.trim(),
    });
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text('编辑武器信息（${widget.initial['id']}）'),
      content: SizedBox(
        width: 480,
        child: Form(
          key: form,
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text(
                  '编号、供体、子类与模型不可修改；改动在「应用到游戏」后写入配置包。',
                ),
                const SizedBox(height: 16),
                TextFormField(
                  controller: name,
                  decoration: const InputDecoration(
                    labelText: '武器名称',
                    helperText: '1–24 字',
                  ),
                  validator: (text) {
                    final value = (text ?? '').trim();
                    if (value.isEmpty || value.runes.length > 24) {
                      return '名称需为 1–24 个字符';
                    }
                    if (value.contains('\t')) return '名称不能包含制表符';
                    return null;
                  },
                ),
                const SizedBox(height: 14),
                Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(
                      child: TextFormField(
                        controller: icon,
                        decoration: const InputDecoration(
                          labelText: '图标',
                          helperText: '相对 Data/UI 的路径；留空则复用供体图标',
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Padding(
                      padding: const EdgeInsets.only(top: 6),
                      child: OutlinedButton.icon(
                        onPressed: pickAndUploadIcon,
                        icon: const Icon(Icons.upload_file, size: 16),
                        label: const Text('上传图片'),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 14),
                TextFormField(
                  controller: description,
                  maxLength: 200,
                  minLines: 1,
                  maxLines: 3,
                  decoration: const InputDecoration(
                    labelText: '武器简介',
                    helperText: '游戏内展示的武器说明；留空则显示武器名称',
                  ),
                ),
                const SizedBox(height: 14),
                TextFormField(
                  controller: note,
                  maxLength: 200,
                  decoration: const InputDecoration(
                    labelText: '备注（仅本地记录）',
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        FilledButton(onPressed: submit, child: const Text('保存修改')),
      ],
    );
  }
}

/// Picks the weapon whose combo state machine should be borrowed.
///
/// Only weapons that already own transitions are offered: a donor with an empty
/// table would produce a weapon that still cannot chain.
class _ComboDonorDialog extends StatefulWidget {
  const _ComboDonorDialog({required this.candidates, required this.suggested});

  final List<Map<String, dynamic>> candidates;
  final int suggested;

  @override
  State<_ComboDonorDialog> createState() => _ComboDonorDialogState();
}

class _ComboDonorDialogState extends State<_ComboDonorDialog> {
  String query = '';

  @override
  Widget build(BuildContext context) {
    final suggestedFirst = [...widget.candidates]..sort((a, b) {
      if (a['id'] == widget.suggested) return -1;
      if (b['id'] == widget.suggested) return 1;
      return (a['id'] as int).compareTo(b['id'] as int);
    });
    final matches = suggestedFirst.where((w) {
      final text = query.trim();
      return text.isEmpty ||
          '${w['name']} ${w['id']}'.contains(text);
    }).toList();
    return AlertDialog(
      title: const Text('选择参考武器（借用它的连招表）'),
      content: SizedBox(
        width: 520,
        height: 460,
        child: Column(
          children: [
            if (widget.suggested > 0)
              Padding(
                padding: const EdgeInsets.only(bottom: 10),
                child: Text(
                  '推荐参考武器编号 ${widget.suggested}：它与当前武器的动作行一致，'
                  '通常就是当初的模板。',
                  style: const TextStyle(fontSize: 12, height: 1.5),
                ),
              ),
            TextField(
              decoration: const InputDecoration(
                labelText: '搜索名称 / 编号',
                prefixIcon: Icon(Icons.search),
              ),
              onChanged: (value) => setState(() => query = value),
            ),
            const SizedBox(height: 10),
            Expanded(
              child: ListView.builder(
                itemCount: matches.length,
                itemBuilder: (context, index) {
                  final value = matches[index];
                  final recommended = value['id'] == widget.suggested;
                  return ListTile(
                    dense: true,
                    selected: recommended,
                    leading: recommended
                        ? const Icon(Icons.star, color: Colors.amber)
                        : null,
                    title: Text('${value['name']}'),
                    subtitle: Text(
                      '${value['type'] ?? '未分类'} · ${value['id']} · '
                      '${value['combo_rows']} 条连招',
                    ),
                    onTap: () => Navigator.pop(context, value['id'] as int),
                  );
                },
              ),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
      ],
    );
  }
}

/// Picks which client folder the editor works on.
///
/// Detection only covers folders next to the server tree, so a manual path
/// field is always offered as a fallback. A plain ListTile-based list is used
/// instead of RadioListTile to stay clear of the Radio API churn.
class _ClientPickerDialog extends StatefulWidget {
  const _ClientPickerDialog({required this.current, required this.detected});

  final String current;
  final List<Map<String, dynamic>> detected;

  @override
  State<_ClientPickerDialog> createState() => _ClientPickerDialogState();
}

class _ClientPickerDialogState extends State<_ClientPickerDialog> {
  String chosen = '';
  final manual = TextEditingController();

  @override
  void initState() {
    super.initState();
    chosen = widget.current;
  }

  @override
  void dispose() {
    manual.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('选择客户端所在文件夹'),
      content: SizedBox(
        width: 660,
        height: 430,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('自动找到的客户端：', style: TextStyle(fontSize: 12)),
            const SizedBox(height: 2),
            Expanded(
              child: widget.detected.isEmpty
                  ? const Center(
                      child: Text('没有自动找到客户端，请在下面直接填路径'),
                    )
                  : ListView.builder(
                      itemCount: widget.detected.length,
                      itemBuilder: (context, index) {
                        final entry = widget.detected[index];
                        final directory = '${entry['directory']}';
                        final valid = entry['valid'] == true;
                        final hash = '${entry['config_hash'] ?? ''}';
                        final problem = '${entry['problem'] ?? ''}';
                        final selected = chosen.trim() == directory;
                        return ListTile(
                          dense: true,
                          enabled: valid,
                          selected: selected,
                          leading: Icon(
                            selected
                                ? Icons.radio_button_checked
                                : Icons.radio_button_unchecked,
                            size: 18,
                          ),
                          title: Row(
                            children: [
                              Text('${entry['label']}'),
                              if (entry['current'] == true) ...[
                                const SizedBox(width: 8),
                                const Text(
                                  '（当前使用）',
                                  style: TextStyle(
                                    fontSize: 11,
                                    color: Colors.teal,
                                  ),
                                ),
                              ],
                              if (!valid) ...[
                                const SizedBox(width: 8),
                                const Text(
                                  '（客户端不可用）',
                                  style: TextStyle(
                                    fontSize: 11,
                                    color: Colors.deepOrange,
                                  ),
                                ),
                              ],
                            ],
                          ),
                          subtitle: Text(
                            // 不可用时把后端给的具体原因显示出来，否则「不是客户端目录」
                            // 会把"文件不存在""文件被改坏"混成一句，只能靠猜。
                            !valid
                                ? '$directory\n${problem.isEmpty ? '无法作为客户端目录' : problem}'
                                : (hash.isEmpty
                                      ? directory
                                      : '$directory\nconfig.spf2  ${hash.substring(0, 12)}…'),
                            style: TextStyle(
                              fontSize: 11,
                              color: valid
                                  ? null
                                  : Colors.deepOrange.shade900,
                            ),
                          ),
                          isThreeLine: hash.isNotEmpty || !valid,
                          onTap: valid
                              ? () => setState(() {
                                    chosen = directory;
                                    manual.text = directory;
                                  })
                              : null,
                        );
                      },
                    ),
            ),
            const SizedBox(height: 8),
            TextField(
              controller: manual,
              decoration: const InputDecoration(
                labelText: '或者直接填写文件夹路径',
                hintText: r'例如 D:/OpenKFO/local-client',
                prefixIcon: Icon(Icons.edit),
              ),
              onChanged: (value) => setState(() => chosen = value.trim()),
            ),
            const SizedBox(height: 6),
            const Text(
              '所选目录里必须有能解析的 Data/config.spf2，否则会被拒绝。',
              style: TextStyle(fontSize: 11),
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
          onPressed: chosen.trim().isEmpty
              ? null
              : () => Navigator.pop(context, chosen.trim()),
          child: const Text('使用此客户端'),
        ),
      ],
    );
  }
}

/// 复用模板：先选武器，再选它的一个状态，返回 {weapon, state}。
class _RemapTemplateDialog extends StatefulWidget {
  const _RemapTemplateDialog({required this.weapons, required this.self});

  final List<Map<String, dynamic>> weapons;
  final int self;

  @override
  State<_RemapTemplateDialog> createState() => _RemapTemplateDialogState();
}

class _RemapTemplateDialogState extends State<_RemapTemplateDialog> {
  String query = '';
  Map<String, dynamic>? selected;
  String? chosenState;

  @override
  Widget build(BuildContext context) {
    final matches = widget.weapons.where((w) {
      final q = query.trim();
      return q.isEmpty || '${w['name']} ${w['id']}'.contains(q);
    }).toList();
    return AlertDialog(
      title: const Text('复用模板：先选武器，再选它的状态'),
      content: SizedBox(
        width: 620,
        height: 500,
        child: Column(
          children: [
            TextField(
              decoration: const InputDecoration(
                labelText: '搜索武器名称 / 编号',
                prefixIcon: Icon(Icons.search),
              ),
              onChanged: (v) => setState(() => query = v),
            ),
            const SizedBox(height: 8),
            Expanded(
              child: Row(
                children: [
                  SizedBox(
                    width: 250,
                    child: ListView.builder(
                      itemCount: matches.length,
                      itemBuilder: (context, i) {
                        final w = matches[i];
                        return ListTile(
                          dense: true,
                          selected: selected?['id'] == w['id'],
                          title: Text('${w['name']}'),
                          subtitle: Text(
                            '${w['type'] ?? ''} · ${w['id']} · '
                            '${(w['stages'] as List).length} 个状态',
                          ),
                          onTap: () => setState(() {
                            selected = w;
                            chosenState = null;
                          }),
                        );
                      },
                    ),
                  ),
                  const VerticalDivider(width: 1),
                  Expanded(
                    child: selected == null
                        ? const Center(child: Text('先选择一把武器'))
                        : ListView.builder(
                            itemCount: (selected!['stages'] as List).length,
                            itemBuilder: (context, i) {
                              final s = selected!['stages'][i];
                              final stateKey = '${s['state']}';
                              final ids = (s['property_ids'] as List? ?? [])
                                  .join('、');
                              final fx = (s['effects'] as List? ?? [])
                                  .join('、');
                              return ListTile(
                                dense: true,
                                selected: chosenState == stateKey,
                                title: Text('${s['label'] ?? stateKey}'),
                                subtitle: Text(
                                  '状态 $stateKey · 动作 ${s['action']}'
                                  '${ids.isEmpty ? '' : '\n命中属性 $ids'}'
                                  '${fx.isEmpty ? '' : '\n特效 $fx'}',
                                  style: const TextStyle(fontSize: 11),
                                ),
                                isThreeLine:
                                    ids.isNotEmpty || fx.isNotEmpty,
                                onTap: () =>
                                    setState(() => chosenState = stateKey),
                              );
                            },
                          ),
                  ),
                ],
              ),
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
          onPressed: (selected == null || chosenState == null)
              ? null
              : () => Navigator.pop(context, {
                    'weapon': selected!['id'] as int,
                    'state': int.parse(chosenState!),
                  }),
          child: const Text('复用此状态'),
        ),
      ],
    );
  }
}

/// 命中属性模板选择器，返回 SkillProId。
class _PropertyPickerDialog extends StatefulWidget {
  const _PropertyPickerDialog({required this.properties});

  final List<Map<String, dynamic>> properties;

  @override
  State<_PropertyPickerDialog> createState() => _PropertyPickerDialogState();
}

class _PropertyPickerDialogState extends State<_PropertyPickerDialog> {
  String query = '';

  @override
  Widget build(BuildContext context) {
    final matches = widget.properties.where((p) {
      final q = query.trim();
      return q.isEmpty || '${p['id']} ${p['summary']}'.contains(q);
    }).toList();
    return AlertDialog(
      title: const Text('选择命中属性模板（按此复制新节点）'),
      content: SizedBox(
        width: 600,
        height: 480,
        child: Column(
          children: [
            TextField(
              decoration: const InputDecoration(
                labelText: '搜索编号 / 摘要',
                prefixIcon: Icon(Icons.search),
              ),
              onChanged: (v) => setState(() => query = v),
            ),
            const SizedBox(height: 8),
            Expanded(
              child: ListView.builder(
                itemCount: matches.length,
                itemBuilder: (context, i) {
                  final p = matches[i];
                  return ListTile(
                    dense: true,
                    title: Text(
                      '${p['id']}',
                      style: const TextStyle(fontFamily: 'monospace'),
                    ),
                    subtitle: Text('${p['summary']}'),
                    onTap: () => Navigator.pop(context, '${p['id']}'),
                  );
                },
              ),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
      ],
    );
  }
}


/// 本地图片路径输入框：选择要上传的 PNG 文件路径。
class _IconUploadDialog extends StatefulWidget {
  const _IconUploadDialog();

  @override
  State<_IconUploadDialog> createState() => _IconUploadDialogState();
}

class _IconUploadDialogState extends State<_IconUploadDialog> {
  final path = TextEditingController();

  @override
  void dispose() {
    path.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('上传本地图片'),
      content: SizedBox(
        width: 480,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('填写本地 PNG 图片的完整路径，会复制到客户端的图标目录。'),
            const SizedBox(height: 12),
            TextField(
              controller: path,
              decoration: const InputDecoration(
                labelText: '图片路径',
                hintText: r'例如 D:\icon\myweapon.png',
                prefixIcon: Icon(Icons.image_outlined),
              ),
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
          onPressed: () => Navigator.pop(context, path.text.trim()),
          child: const Text('上传'),
        ),
      ],
    );
  }
}

/// 合并包导出范围选择：只导当前武器 / 全部自建武器。
class _MergeExportDialog extends StatelessWidget {
  const _MergeExportDialog({
    required this.weaponName,
    required this.createdCount,
  });
  final String weaponName;
  final int createdCount;

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('导出合并包'),
      content: SizedBox(
        width: 480,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text(
              '合并包只装这把武器自己的配置条目（item.txt / itemact.txt 各一行、'
              '连招转移、特效登记、连招限制、动作块、命中属性）和它引用的素材，'
              '不含整份 config.spf2。导入时只合并这些条目，目标客户端其余配置'
              '一个字节都不动。导入前会先列出新增和会被覆盖的武器，确认后才写入。',
            ),
            const SizedBox(height: 12),
            if (createdCount > 1)
              const Text(
                '选择导出范围：',
                style: TextStyle(fontSize: 12),
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
          onPressed: () => Navigator.pop(context, false),
          child: Text('只导「$weaponName」'),
        ),
        if (createdCount > 1)
          FilledButton.tonal(
            onPressed: () => Navigator.pop(context, true),
            child: Text('全部自建（$createdCount 把）'),
          ),
      ],
    );
  }
}

/// 合并包导入预览：以所选客户端的 Data/config.spf2 为参照，列出包里哪些武器是
/// 新增、哪些编号已存在会被覆盖。用户点「确定导入」后才真正写盘。
class _MergePreviewDialog extends StatelessWidget {
  const _MergePreviewDialog({required this.preview});
  final Map<String, dynamic> preview;

  @override
  Widget build(BuildContext context) {
    final reference = '${preview['reference'] ?? ''}';
    final newWeapons = [
      for (final w in (preview['new'] as List? ?? []))
        Map<String, dynamic>.from(w as Map),
    ];
    final modified = [
      for (final w in (preview['modified'] as List? ?? []))
        Map<String, dynamic>.from(w as Map),
    ];
    final total = (preview['weapons'] as List? ?? []).length;
    String label(Map<String, dynamic> w) => '${w['name']}（${w['id']}）';
    return AlertDialog(
      title: const Text('确认导入合并包'),
      content: SizedBox(
        width: 560,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text(
                '下面是这个合并包导入到当前客户端后的变化。确认无误后点「确定导入」，'
                '导入前会自动备份 config.spf2，目标客户端的其他配置不会被改动。',
              ),
              const SizedBox(height: 10),
              Text(
                '参照配置：$reference',
                style: const TextStyle(fontSize: 11, color: Colors.black54),
              ),
              const SizedBox(height: 6),
              Text(
                '包里共 $total 把武器。',
                style: const TextStyle(fontSize: 12, fontWeight: FontWeight.bold),
              ),
              if (newWeapons.isNotEmpty) ...[
                const SizedBox(height: 10),
                Text(
                  '新增武器 ${newWeapons.length} 把：',
                  style: const TextStyle(fontSize: 12, color: Colors.green),
                ),
                const SizedBox(height: 2),
                for (final w in newWeapons)
                  Padding(
                    padding: const EdgeInsets.only(left: 8, bottom: 1),
                    child: Text(
                      '  · ${label(w)}',
                      style: const TextStyle(fontSize: 12, color: Colors.green),
                    ),
                  ),
              ],
              if (modified.isNotEmpty) ...[
                const SizedBox(height: 10),
                Text(
                  '已存在、导入后会覆盖的武器 ${modified.length} 把：',
                  style: const TextStyle(fontSize: 12, color: Colors.deepOrange),
                ),
                const SizedBox(height: 2),
                for (final w in modified)
                  Padding(
                    padding: const EdgeInsets.only(left: 8, bottom: 1),
                    child: Text(
                      '  · ${label(w)}',
                      style: const TextStyle(
                        fontSize: 12,
                        color: Colors.deepOrange,
                      ),
                    ),
                  ),
                const SizedBox(height: 4),
                const Text(
                  '这些编号在当前客户端已存在，合并包会按自己的配置覆盖它们的'
                  '武器行、动作行、连招、特效与动作块。',
                  style: TextStyle(fontSize: 11, color: Colors.deepOrange),
                ),
              ],
              if (newWeapons.isEmpty && modified.isEmpty) ...[
                const SizedBox(height: 10),
                const Text(
                  '包里没有可导入的武器。',
                  style: TextStyle(fontSize: 12, color: Colors.deepOrange),
                ),
              ],
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context, false),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: (newWeapons.isEmpty && modified.isEmpty)
              ? null
              : () => Navigator.pop(context, true),
          child: const Text('确定导入'),
        ),
      ],
    );
  }
}

/// 合并包导入：列出最近导出的合并包供点选，也允许手填 zip 路径。
class _MergeImportDialog extends StatefulWidget {
  const _MergeImportDialog({required this.loadPackages});

  final Future<Map<String, dynamic>> Function() loadPackages;

  @override
  State<_MergeImportDialog> createState() => _MergeImportDialogState();
}

class _MergeImportDialogState extends State<_MergeImportDialog> {
  final path = TextEditingController();
  List<Map<String, dynamic>> packages = [];
  String directory = '';
  String note = '';
  bool loading = true;

  @override
  void initState() {
    super.initState();
    // 监听而不是 onChanged：粘贴、程序填值也要让「预览变更」跟着亮起来。
    path.addListener(() => setState(() {}));
    loadPackages();
  }

  @override
  void dispose() {
    path.dispose();
    super.dispose();
  }

  Future<void> loadPackages() async {
    try {
      final result = await widget.loadPackages();
      if (!mounted) return;
      setState(() {
        directory = '${result['directory'] ?? ''}';
        packages = [
          for (final p in (result['packages'] as List? ?? []))
            Map<String, dynamic>.from(p as Map),
        ];
        loading = false;
        note = packages.isEmpty ? '这个目录里还没有合并包；先在武器页点「导出合并包」。' : '';
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        loading = false;
        note = '读取合并包列表失败：$e';
      });
    }
  }

  static String sizeText(dynamic bytes) {
    final value = (bytes is num) ? bytes.toDouble() : 0.0;
    if (value >= 1024 * 1024) return '${(value / 1024 / 1024).toStringAsFixed(2)} MB';
    if (value >= 1024) return '${(value / 1024).toStringAsFixed(1)} KB';
    return '${value.toStringAsFixed(0)} B';
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('导入武器包'),
      content: SizedBox(
        width: 620,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text(
              '导入会先以当前客户端的 Data/config.spf2 为参照，列出包里哪些武器是'
              '新增、哪些编号已存在会被覆盖，确认后再合并。只合并包里武器自己的配置'
              '条目，目标客户端的其他配置不会被改动；写入前会自动备份。',
            ),
            const SizedBox(height: 10),
            if (loading) const LinearProgressIndicator(),
            if (packages.isNotEmpty) ...[
              const Text('最近导出的合并包（点一下即选中）：', style: TextStyle(fontSize: 12)),
              const SizedBox(height: 4),
              SizedBox(
                height: 168,
                child: ListView.builder(
                  itemCount: packages.length,
                  itemBuilder: (context, index) {
                    final item = packages[index];
                    final value = '${item['path']}';
                    final selected = path.text.trim() == value;
                    return ListTile(
                      dense: true,
                      selected: selected,
                      leading: Icon(
                        selected
                            ? Icons.radio_button_checked
                            : Icons.radio_button_unchecked,
                        size: 18,
                      ),
                      title: Text(
                        '${item['name']}',
                        style: const TextStyle(fontSize: 12),
                      ),
                      subtitle: Text(
                        '${item['modified']} · ${sizeText(item['size'])}',
                        style: const TextStyle(fontSize: 11),
                      ),
                      onTap: () => setState(() => path.text = value),
                    );
                  },
                ),
              ),
              const SizedBox(height: 8),
            ],
            TextField(
              controller: path,
              decoration: const InputDecoration(
                labelText: '合并包路径',
                hintText: r'例如 D:\OpenKFO\server\dist\weapon-packages\weapon-merge-253300-*.zip',
                prefixIcon: Icon(Icons.file_upload_outlined),
              ),
            ),
            if (note.isNotEmpty) ...[
              const SizedBox(height: 6),
              Text(
                note,
                style: const TextStyle(fontSize: 11, color: Colors.deepOrange),
              ),
            ],
            if (directory.isNotEmpty) ...[
              const SizedBox(height: 4),
              Text(
                '合并包目录：$directory',
                style: const TextStyle(fontSize: 11, color: Colors.black54),
              ),
            ],
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: path.text.trim().isEmpty
              ? null
              : () => Navigator.pop(context, path.text.trim()),
          child: const Text('预览变更'),
        ),
      ],
    );
  }
}

/// 每个状态的可编辑重映射：动作 / 命中属性 / 说明三个输入框；复用模板把结果
/// 填进输入框，用户可再改，然后提交或取消。
class _RemapEditor extends StatefulWidget {
  const _RemapEditor({
    required this.stateKey,
    required this.weaponId,
    required this.initial,
    required this.busy,
    required this.api,
    required this.weapons,
    required this.onSaved,
    super.key,
  });

  final String stateKey;
  final int weaponId;
  final Map<String, dynamic> initial;
  final bool busy;
  final Future<dynamic> Function(Map<String, dynamic>) api;
  final List<Map<String, dynamic>> weapons;
  final Future<void> Function() onSaved;

  @override
  State<_RemapEditor> createState() => _RemapEditorState();
}

class _RemapEditorState extends State<_RemapEditor> {
  late final TextEditingController action;
  late final TextEditingController property;
  late final TextEditingController label;
  bool saving = false;

  @override
  void initState() {
    super.initState();
    action = TextEditingController(text: '${widget.initial['action'] ?? ''}');
    property =
        TextEditingController(text: '${widget.initial['property_id'] ?? ''}');
    label = TextEditingController(text: '${widget.initial['label'] ?? ''}');
  }

  @override
  void dispose() {
    action.dispose();
    property.dispose();
    label.dispose();
    super.dispose();
  }

  bool get engaged => saving || widget.busy;

  Future<void> pickTemplate() async {
    final picked = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _RemapTemplateDialog(
        weapons: widget.weapons,
        self: widget.weaponId,
      ),
    );
    if (picked == null || !mounted) return;
    try {
      final r = Map<String, dynamic>.from(await widget.api({
        'operation': 'weapon_template_resolve',
        'template_weapon': picked['weapon'],
        'template_stage': picked['state'],
      }));
      if (!mounted) return;
      setState(() {
        action.text = '${r['action'] ?? ''}';
        property.text = '${r['property_id'] ?? ''}';
      });
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('$e')));
      }
    }
  }

  Future<void> addProperty() async {
    Map<String, dynamic> catalog = {};
    try {
      catalog = Map<String, dynamic>.from(
        await widget.api({'operation': 'weapon_remap_options'}),
      );
    } catch (_) {}
    final properties = [
      for (final p in (catalog['properties'] as List? ?? []))
        Map<String, dynamic>.from(p as Map),
    ];
    final template = await showDialog<String>(
      context: context,
      builder: (_) => _PropertyPickerDialog(properties: properties),
    );
    if (template == null || !mounted) return;
    try {
      final result = Map<String, dynamic>.from(await widget.api({
        'operation': 'weapon_property_add',
        'template': template,
      }));
      if (!mounted) return;
      final newId = '${result['property_id'] ?? ''}';
      if (newId.isEmpty) return;
      setState(() => property.text = newId);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('$e')));
      }
    }
  }

  Future<void> commit({bool clear = false}) async {
    setState(() => saving = true);
    try {
      final result = Map<String, dynamic>.from(await widget.api({
        'operation': 'weapon_remap',
        'weapon': widget.weaponId,
        'stage': int.parse(widget.stateKey),
        'action': clear ? '' : action.text.trim(),
        'property_id': clear ? '' : property.text.trim(),
        'label': clear ? '' : label.text.trim(),
      }));
      if (!mounted) return;
      if (clear) {
        setState(() {
          action.clear();
          property.clear();
          label.clear();
        });
      }
      setState(() => saving = false);
      await widget.onSaved();
      if (mounted && result['message'] != null) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('${result['message']}')));
      }
    } catch (e) {
      if (mounted) {
        setState(() => saving = false);
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('$e')));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: Colors.blueGrey.shade50,
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text(
            '动作与命中属性（重映射）',
            style: TextStyle(fontWeight: FontWeight.w600, fontSize: 13),
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: TextField(
                  controller: action,
                  decoration: const InputDecoration(
                    labelText: '动作 ID',
                    isDense: true,
                    helperText: '如 2001130；留空沿用原动作',
                  ),
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: TextField(
                  controller: property,
                  decoration: const InputDecoration(
                    labelText: '命中属性 ID',
                    isDense: true,
                    helperText: '如 80810；留空沿用动作自带',
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          TextField(
            controller: label,
            decoration: const InputDecoration(
              labelText: '动作说明（可选）',
              isDense: true,
            ),
          ),
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            runSpacing: 4,
            children: [
              OutlinedButton.icon(
                onPressed: engaged ? null : pickTemplate,
                icon: const Icon(Icons.content_copy, size: 16),
                label: const Text('复用模板填入'),
              ),
              OutlinedButton.icon(
                onPressed: engaged ? null : addProperty,
                icon: const Icon(Icons.add_box_outlined, size: 16),
                label: const Text('新增命中属性节点'),
              ),
              FilledButton.icon(
                onPressed: engaged ? null : () => commit(),
                icon: const Icon(Icons.check, size: 16),
                label: const Text('提交重映射'),
              ),
              TextButton(
                onPressed: engaged ? null : () => commit(clear: true),
                child: const Text('取消重映射'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

