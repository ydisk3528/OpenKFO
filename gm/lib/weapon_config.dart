import 'item_pictures.dart';
import 'weapon_merge_page.dart';
import 'weapon_workspace.dart';

import 'dart:convert';

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

const _ruleFieldKeys = <String>{
  'SkillDamage',
  'SkillEnhanceDamage',
  'RepulseTarget',
  'TripTarget',
  'TargetFlurr',
  'StandHurt',
  'StandHurtDown',
  'StandHurtFly',
  'FlyHurt',
  'JumpHurtDown',
  'JumpHurtFall',
};

Map<String, dynamic> _extractHitValues(Object? value) {
  if (value is Map && value['values'] is Map) {
    return {
      for (final entry in (value['values'] as Map).entries)
        '${entry.key}': entry.value,
    };
  }
  if (value is Map) {
    const metadata = <String>{
      'id',
      'values',
      'buff',
      'variant',
      'references',
      'owner_weapon',
      'action',
      'state',
      'condition',
      'segment_id',
      'kind',
      'template',
      'source',
      'preallocated',
      'removed',
    };
    return {
      for (final entry in value.entries)
        if (!metadata.contains('${entry.key}')) '${entry.key}': entry.value,
    };
  }
  return {};
}

Map<String, dynamic> _mergeHitValueMaps(Iterable<Object?> sources) {
  final result = <String, dynamic>{};
  for (final source in sources) {
    result.addAll(_extractHitValues(source));
  }
  return result;
}

class _WeaponConfigPageState extends State<WeaponConfigPage> {
  Map<String, dynamic>? data, weapon;
  WeaponWorkspace? workspace;
  Map<String, dynamic> remaps = {}, cleared = {}, extraProperties = {};

  Map<String, dynamic> _hitValues(Object? value) => _extractHitValues(value);

  Map<String, dynamic> _mergeHitValues(Iterable<Object?> sources) =>
      _mergeHitValueMaps(sources);

  dynamic _hitField(String id, String field, [dynamic fallback]) {
    return _hitValues(hitProperties[id])[field] ?? fallback;
  }

  Map<String, dynamic> _ruleValues(Object? value) {
    final values = _hitValues(value);
    final result = <String, dynamic>{};
    for (final entry in values.entries) {
      final parsed = entry.value is num
          ? entry.value as num
          : num.tryParse('${entry.value}'.trim());
      if (_ruleFieldKeys.contains('${entry.key}') &&
        parsed != null &&
        parsed.isFinite) {
        result['${entry.key}'] = parsed;
      }
    }
    return result;
  }

  /// 命中属性的唯一内存来源；rules.properties 仅作为旧数据兼容投影。
  Map<String, dynamic> hitProperties = {};
  Map<String, dynamic> _structureBaseline = {};
  Map<String, dynamic> _clientInfo = {};
  final form = GlobalKey<FormState>();
  List<Map<String, dynamic>> rules = [];
  String query = '', message = '';
  String weaponType = '全部类型';
  bool busy = true, dirty = false, failed = false;
  bool workspaceExists = false;
  String workspaceSavedAt = '';
  String? selectedAction;
  int editorVersion = 0;
  int _loadGeneration = 0;
  int _selectionGeneration = 0;

  /// 非自建武器「启用编辑」开关：每次重新选中武器都会复位，需要再次手动开启。
  bool editUnlocked = false;

  /// 最近一次导出发版包的结果（后端 packageResult）；空 map 表示还没导出过。
  Map<String, dynamic> exportResult = {};

  /// 最近一次导出是不是合并包（只含当前武器的配置）。
  bool lastExportMerge = false;

  /// 最近一次「导入武器包」的结果（后端 mergeImportReport）；空 map 表示还没导入过。
  Map<String, dynamic> mergeImportResult = {};

  @override
  void initState() {
    super.initState();
    load(refresh: false);
  }

  Future<void> load({int? prefer, bool refresh = true}) async {
    final generation = ++_loadGeneration;
    final selectedId = prefer ?? weapon?['id'] as int?;
    final selectionGeneration = ++_selectionGeneration;
    final profile = activeProfile;
    final hash = sourceHash;
    setState(() {
      clearWeaponDetails();
      weapon = null;
      rules = [];
      busy = true;
      failed = false;
    });
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({'operation': 'weapon_list', '_refresh': refresh}),
      );
      Map<String, dynamic> client = {};
      try {
        client = Map<String, dynamic>.from(
          await widget.api({'operation': 'client_directory_get'}),
        );
      } catch (_) {
        // The card falls back to whatever was shown before.
      }
      if (generation != _loadGeneration ||
          !_sameWeaponContext(selectionGeneration, null, profile, hash)) return;
      final selected = (result['weapons'] as List).where(
        (w) => selectedId != null && w['id'] == selectedId,
      );
      setState(() {
        data = result;
        if (client.isNotEmpty) _clientInfo = client;
        ustateOptions = [
          for (final u in (result['ustates'] as List? ?? []))
            {'id': '${(u as Map)['id']}', 'name': '${u['name'] ?? ''}'},
        ];
        clearWeaponDetails();
        weapon = null;
        rules = [];
        dirty = false;
        busy = selected.isNotEmpty;
      });
      if (selected.isNotEmpty) {
        await select(Map<String, dynamic>.from(selected.first));
      }
    } catch (e) {
      if (generation == _loadGeneration &&
          _sameWeaponContext(selectionGeneration, null, profile, hash)) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  Future<void> reloadSelectedWeapon({int? prefer}) async {
    final selected = weapon;
    if (selected != null && (prefer == null || selected['id'] == prefer)) {
      await select(Map<String, dynamic>.from(selected));
    }
  }

  Future<void> refreshWorkspaceStatus() async {
    final selected = weapon;
    final generation = _selectionGeneration;
    final profile = activeProfile;
    final hash = sourceHash;
    if (selected == null) return;
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({
          'operation': 'weapon_workspace_status',
          'weapon': selected['id'],
          ..._profileContextFor(profile, hash),
        }),
      );
      if (!_sameWeaponContext(generation, selected['id'], profile, hash)) return;
      _validateProfileResponse(result, profile, hash);
      setState(() {
        workspaceExists = result['exists'] == true;
        workspaceSavedAt =
            '${result['payload'] is Map ? (result['payload'] as Map)['saved_at'] ?? '' : ''}';
      });
    } catch (_) {
      if (_sameWeaponContext(generation, selected['id'], profile, hash))
        setState(() {
          workspaceExists = false;
          workspaceSavedAt = '';
        });
    }
  }

  Future<void> saveWorkspace() async {
    final selected = weapon;
    final generation = _selectionGeneration;
    final profile = activeProfile;
    final hash = sourceHash;
    if (selected == null || busy) return;
    final currentWorkspace = _snapshotWorkspace();
    setState(() {
      workspace = currentWorkspace;
      busy = true;
      failed = false;
      message = '正在保存当前武器暂存副本…';
    });
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({
          'operation': 'weapon_workspace_save',
          'weapon': selected['id'],
          ..._profileContextFor(profile, hash),
          'workspace': currentWorkspace.toJson(),
        }),
      );
      if (!mounted ||
          generation != _selectionGeneration ||
          weapon?['id'] != selected['id'] ||
          activeProfile != profile ||
          sourceHash != hash) return;
      _validateProfileResponse(result, profile, hash);
      setState(() {
        busy = false;
        workspaceExists = true;
        workspaceSavedAt = '${result['saved_at'] ?? ''}';
        dirty = false;
        message = '${result['message'] ?? '当前武器暂存已保存'}';
      });
    } catch (e) {
      if (mounted &&
          generation == _selectionGeneration &&
          weapon?['id'] == selected['id'] &&
          activeProfile == profile &&
          sourceHash == hash)
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
    }
  }

  Future<void> loadWorkspace() async {
    final selected = weapon;
    final generation = _selectionGeneration;
    final profile = activeProfile;
    final hash = sourceHash;
    if (selected == null || busy) return;
    setState(() {
      busy = true;
      failed = false;
      message = '正在检查并加载当前武器暂存副本…';
    });
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({
          'operation': 'weapon_workspace_load',
          'weapon': selected['id'],
          ..._profileContextFor(profile, hash),
        }),
      );
      if (result['exists'] != true || result['payload'] is! Map) {
        throw StateError('当前武器没有可加载的暂存副本');
      }
      final restored = WeaponWorkspace.fromJson(
        Map<String, dynamic>.from(result['payload'] as Map),
      );
      if (!mounted ||
          generation != _selectionGeneration ||
          weapon?['id'] != selected['id'] ||
          activeProfile != profile ||
          sourceHash != hash) return;
      _validateProfileResponse(result, profile, hash);
      setState(() {
        _restoreWorkspace(restored);
        workspace = restored;
        workspaceExists = true;
        workspaceSavedAt = '${(result['payload'] as Map)['saved_at'] ?? ''}';
        dirty = true;
        busy = false;
        failed = false;
        message = '暂存副本已加载到当前武器内存；点击“应用到游戏”才会写入客户端。';
      });
    } catch (e) {
      if (mounted &&
          generation == _selectionGeneration &&
          weapon?['id'] == selected['id'] &&
          activeProfile == profile &&
          sourceHash == hash)
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
    }
  }

  Future<void> deleteWorkspace() async {
    final selected = weapon;
    final generation = _selectionGeneration;
    final profile = activeProfile;
    final hash = sourceHash;
    if (selected == null || busy || !workspaceExists) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('删除暂存副本？'),
        content: const Text('只删除当前武器的本地 JSON 暂存，不会修改客户端配置。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (confirmed != true || !_sameWeaponContext(generation, selected['id'], profile, hash)) return;
    setState(() {
      busy = true;
      message = '正在删除当前武器暂存副本…';
    });
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({
          'operation': 'weapon_workspace_delete',
          'weapon': selected['id'],
          ..._profileContextFor(profile, hash),
        }),
      );
      if (!_sameWeaponContext(generation, selected['id'], profile, hash)) return;
      _validateProfileResponse(result, profile, hash);
      setState(() {
        busy = false;
        workspaceExists = false;
        workspaceSavedAt = '';
        message = '${result['message'] ?? '暂存已删除'}';
      });
    } catch (e) {
      if (_sameWeaponContext(generation, selected['id'], profile, hash))
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
    }
  }

  /// Self-made weapons registered in settings.json, keyed by weapon id.
  /// They exist only as rows injected into item.txt / itemact.txt, so the
  /// client binary stays untouched.
  Map<String, dynamic> get created =>
      Map<String, dynamic>.from(data?['created'] as Map? ?? const {});

  bool isCreated(dynamic id) => created.containsKey('$id');

  /// 编辑按钮的显示开关：自建武器直接可编辑；非自建武器需先点「启用编辑」。
  bool canEdit(dynamic id) => isCreated(id) || editUnlocked;

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

  String get activeProfile => '${data?['active_profile'] ?? ''}';
  String get sourceHash => '${data?['source_hash'] ?? ''}';

  Map<String, dynamic> _profileContextFor(String profile, String hash) => {
    if (profile.isNotEmpty) 'active_profile': profile,
    if (hash.isNotEmpty) 'source_hash': hash,
  };

  bool _sameWeaponContext(int generation, dynamic id, String profile, String hash) =>
      mounted && generation == _selectionGeneration && weapon?['id'] == id &&
      activeProfile == profile && sourceHash == hash;

  void _validateProfileResponse(Map result, String profile, String hash) {
    final responseProfile = '${result['active_profile'] ?? ''}';
    final responseHash = '${result['source_hash'] ?? ''}';
    if ((profile.isNotEmpty && responseProfile.isNotEmpty && responseProfile != profile) ||
        (hash.isNotEmpty && responseHash.isNotEmpty && responseHash != hash)) {
      throw StateError('客户端配置档已切换，请刷新武器配置');
    }
  }

  Future<dynamic> Function(Map<String, dynamic>) _apiForWeaponContext(
    int generation, dynamic id, String profile, String hash,
  ) => (request) async {
    if (!_sameWeaponContext(generation, id, profile, hash)) {
      throw StateError('武器或客户端配置档已切换，请重新打开编辑器');
    }
    final result = await widget.api({
      ...request,
      ..._profileContextFor(profile, hash),
    });
    if (!_sameWeaponContext(generation, id, profile, hash)) {
      throw StateError('武器或客户端配置档已切换，请重新打开编辑器');
    }
    if (result is Map) _validateProfileResponse(result, profile, hash);
    return result;
  };

  Future<void> reloadClient() async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({
          'operation': 'client_directory_get',
          ..._profileContextFor(profile, hash),
        }),
      );
      if (!_sameWeaponContext(generation, id, profile, hash)) return;
      _validateProfileResponse(result, profile, hash);
      setState(() => _clientInfo = result);
    } catch (_) {
      // The card simply keeps the previous picture.
    }
  }

  Future<void> chooseClient(/* optional initial pick */) async {
    final previousGeneration = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    final picked = await showDialog<String>(
      context: context,
      builder: (_) => ClientPickerDialog(
        current: '${_clientInfo['directory'] ?? ''}',
        savedTo: '${_clientInfo['saved_to'] ?? ''}',
        detected: [
          for (final value in (_clientInfo['detected'] as List? ?? []))
            Map<String, dynamic>.from(value as Map),
        ],
      ),
    );
    if (picked == null || picked.isEmpty ||
        !_sameWeaponContext(previousGeneration, id, profile, hash)) return;
    final prefer = id as int?;
    final generation = ++_selectionGeneration;
    setState(() {
      clearWeaponDetails();
      weapon = null;
      rules = [];
      busy = true;
      failed = false;
      message = '正在切换客户端…';
    });
    try {
      final result = await widget.api({
        'operation': 'client_directory_set',
        'directory': picked,
        ..._profileContextFor(profile, hash),
      });
      if (!_sameWeaponContext(generation, null, profile, hash)) return;
      setState(() {
        busy = false;
        _clientInfo = Map<String, dynamic>.from(result is Map ? result : {});
        message = '${(result as Map)['message'] ?? '已切换客户端'}';
      });
      await load(prefer: prefer);
    } catch (e) {
      if (_sameWeaponContext(generation, null, profile, hash)) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  Future<void> rebaseClient() async {
    final previousGeneration = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
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
    if (confirmed != true ||
        !_sameWeaponContext(previousGeneration, id, profile, hash)) return;
    final prefer = id as int?;
    final generation = ++_selectionGeneration;
    setState(() {
      clearWeaponDetails();
      weapon = null;
      rules = [];
      busy = true;
      failed = false;
      message = '正在重新采集基线…';
    });
    try {
      final result = await widget.api({
        'operation': 'weapon_client_rebase',
        ..._profileContextFor(profile, hash),
      });
      if (!_sameWeaponContext(generation, null, profile, hash)) return;
      setState(() {
        busy = false;
        message = '${(result as Map)['message'] ?? '已重新采集基线'}';
      });
      await load(prefer: prefer);
    } catch (e) {
      if (_sameWeaponContext(generation, null, profile, hash)) {
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

  /// 动作块内的招架（`<Counter>` + `<TriggerBox>`）：第三条连招通道，
  /// **被对手攻击触发**（不是按键）。每状态至多一条；这份是渲染后的现状
  /// （含已保存的编辑），未编辑时就是供体块里带过来的原样。
  List<Map<String, dynamic>> counters = [];

  /// 本次编辑中改过的状态 → 要写的招架；值为 null 表示"删掉这个状态的招架"。
  Map<String, Map<String, dynamic>?> counterEdits = {};

  /// 后端已保存的招架编辑，用来区分"改过的"和"块里原有的"。
  Map<String, Map<String, dynamic>?> counterSaved = {};
  bool counterEditing = false;

  /// 防护（霸体/无敌/穿人）与自身状态（UState/Ustate/AddBuff）：渲染后的现状，
  /// 按状态分组；未编辑时就是块里原有的（多半是供体带来的）。
  List<Map<String, dynamic>> blockElements = [];

  /// 后端已保存的编辑：状态 → 元素标签 → 条目。
  Map<String, Map<String, List<Map<String, dynamic>>>> blockElementsSaved = {};

  /// 本次编辑改过的那部分，形状同上；某一个 (状态,标签) 出现即为「按这个列表写」，
  /// 空列表 = 删掉该标签。**没出现的一律不碰**。
  Map<String, Map<String, List<Map<String, dynamic>>>> blockElementsEdit = {};

  /// 可写元素的清单（后端 block_element_groups：分组 + 标签 + 中文名 + 是否单实例）。
  List<Map<String, dynamic>> blockElementGroups = [];

  /// 状态目录（ustate.xml）：id + 策划中文名，用来做下拉。
  List<Map<String, String>> ustateOptions = [];
  bool blockElementEditing = false;
  bool frameEditing = false;

  /// 「动作分支」：同一招在**持有某个状态时**换成另一套动作（多段 / 换伤害 / 换 Buff）。
  /// 机制是同名 <AnmDesc> 注册两条块，第二条头部带 <Condition><Ustate id="N"/>。
  /// 这份是**渲染后的现状**：状态号 → 该状态下已有的条件分支。
  Map<String, List<Map<String, dynamic>>> variants = {};

  /// 后端已保存的分支定义：状态号 → 分支列表（与渲染后现状可能不同，是作者态）。
  Map<String, List<Map<String, dynamic>>> variantsSaved = {};

  /// 本次编辑的**增量**：状态号 → 要写/要删的分支（出现即生效；不再出现=不碰）。
  /// 已有分支被改 = 覆盖同 condition；被删 = {condition, remove:true}；新增 = 完整定义。
  Map<String, List<Map<String, dynamic>>> variantsEdit = {};
  bool variantEditing = false;
  List<Map<String, dynamic>>? _variantRulesSnapshot;
  Map<String, dynamic>? _variantHitPropertiesSnapshot;
  bool _variantDirtySnapshot = false;
  Map<String, dynamic>? _variantOccupiedSnapshot;

  Set<String> get localVariantIDs => {
    for (final perState in variantsEdit.values)
      for (final branch in perState)
        for (final segment in (branch['segments'] as List? ?? const []))
          if (isVariantSkillProID(
                '${(segment as Map)['skillproid'] ?? ''}',
                minimum: variantSkillProMin,
                maximum: variantSkillProMax,
              ) &&
              !variantServerOccupiedIDs.contains('${segment['skillproid']}'))
            '${segment['skillproid']}',
  };

  void syncVariantOccupiedIDs() {
    variantOccupiedIDs = {...variantServerOccupiedIDs, ...localVariantIDs};
  }

  /// 各状态**无条件块**的动作段（照抄本招 / 回填参考）：状态号 → 段列表。
  Map<String, List<Map<String, dynamic>>> variantBases = {};
  Set<String> variantOccupiedIDs = {};
  Set<String> variantServerOccupiedIDs = {};
  int variantSkillProMin = 910000000;
  int variantSkillProMax = 910999999;

  /// 帧轨道（weapon_stage_track 的结果）：状态 → 该招式的片断/标记/真实帧数。
  /// 打开某个招式的「帧轨道与攻击范围」弹窗时按武器一次性拉取，然后按状态缓存。
  Map<String, Map<String, dynamic>> stageTracks = {};

  /// 已保存的攻击范围编辑：状态 → 片断编号 → 六个盒尺寸属性。
  Map<String, Map<String, List<Map<String, dynamic>>>> scopeSaved = {};

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

  Future<void> refreshChain(
    dynamic weaponId, {
    Map<String, dynamic>? snapshot,
  }) async {
    final generation = _selectionGeneration;
    final profile = activeProfile;
    final hash = sourceHash;
    try {
      final result =
          snapshot ??
          Map<String, dynamic>.from(
            await widget.api({
              'operation': 'weapon_combo_chain',
              'weapon': weaponId,
              ..._profileContextFor(profile, hash),
            }),
          );
      if (!_sameWeaponContext(generation, weaponId, profile, hash)) return;
      _validateProfileResponse(result, profile, hash);
      setState(() {
        comboChain = [
          for (final e in (result['chain'] as List? ?? []))
            Map<String, String>.from(e as Map),
        ];
        variantServerOccupiedIDs = {
          for (final id in (result['variant_occupied_ids'] as List? ?? []))
            '$id',
        };
        variantOccupiedIDs = {...variantServerOccupiedIDs};
        variantSkillProMin =
            int.tryParse('${result['variant_skillpro_min'] ?? ''}') ??
            variantSkillProMin;
        variantSkillProMax =
            int.tryParse('${result['variant_skillpro_max'] ?? ''}') ??
            variantSkillProMax;
        comboDeadEnds = [
          for (final e in (result['dead_ends'] as List? ?? []))
            Map<String, String>.from(e as Map),
        ];
        frameSwitches = [
          for (final e in (result['frame_switches'] as List? ?? []))
            Map<String, dynamic>.from(e as Map),
        ];
        frameSaved = {
          for (final e
              in (result['frame_switches_saved'] as Map? ?? {}).entries)
            '${e.key}': [
              for (final sw in (e.value as List? ?? []))
                Map<String, dynamic>.from(sw as Map),
            ],
        };
        frameKeys = [
          for (final e in (result['frame_keys'] as List? ?? []))
            {'v': '${(e as Map)['value']}', 'l': '${e['label']}'},
        ];
        counters = [
          for (final e in (result['counters'] as List? ?? []))
            Map<String, dynamic>.from(e as Map),
        ];
        counterSaved = {
          for (final e in (result['counters_saved'] as Map? ?? {}).entries)
            '${e.key}': e.value == null
                ? null
                : Map<String, dynamic>.from(e.value as Map),
        };
        counterEdits = {};
        counterEditing = false;
        blockElements = [
          for (final e in (result['block_elements'] as List? ?? []))
            Map<String, dynamic>.from(e as Map),
        ];
        blockElementsSaved = _decodeBlockElements(
          result['block_elements_saved'] as Map? ?? {},
        );
        blockElementsEdit = {};
        blockElementEditing = false;
        blockElementGroups = [
          for (final g in (result['block_element_groups'] as List? ?? []))
            Map<String, dynamic>.from(g as Map),
        ];
        variants = _decodeVariants(result['variants'] as Map? ?? {});
        variantsSaved = _decodeVariants(result['variants_saved'] as Map? ?? {});
        variantServerOccupiedIDs = {
          for (final id in (result['variant_occupied_ids'] as List? ?? []))
            '$id',
        };
        variantOccupiedIDs = {...variantServerOccupiedIDs};
        variantSkillProMin =
            int.tryParse('${result['variant_skillpro_min'] ?? ''}') ??
            910000000;
        variantSkillProMax =
            int.tryParse('${result['variant_skillpro_max'] ?? ''}') ??
            910999999;
        variantBases = _decodeBases(result['variant_bases'] as Map? ?? {});
        variantsEdit = {};
        variantEditing = false;
        frameEdits = {};
        frameEditing = false;
        comboKeys = [
          for (final e in (result['keys'] as List? ?? []))
            {'v': '${(e as Map)['id']}', 'l': '${e['label']}'},
        ];
        // 轨道/攻击范围随武器重读，避免显示上一把的缓存。
        stageTracks = {};
        scopeSaved = {};
      });
    } catch (error) {
      if (_sameWeaponContext(generation, weaponId, profile, hash)) {
        setState(() {
          failed = true;
          message = '动作分支与连招读取失败：$error';
        });
        setState(() => comboChain = []);
        setState(() => comboDeadEnds = []);
        setState(() => frameSwitches = []);
        setState(() {
          frameSaved = {};
          counters = [];
          counterSaved = {};
          counterEdits = {};
          counterEditing = false;
          frameEdits = {};
          frameEditing = false;
          variants = {};
          variantsSaved = {};
          variantBases = {};
          variantsEdit = {};
          variantEditing = false;
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
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    final added = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _FrameSwitchDialog(
        state: state,
        states: [for (final s in (data?['states'] as List? ?? [])) '$s'],
        keys: frameKeys,
      ),
    );
    if (added == null || !_sameWeaponContext(generation, id, profile, hash)) return;
    final list = [for (final e in frameListFor(state)) e]..add(added);
    setState(() => frameEdits[state] = list);
  }

  /// 将模块编辑合并到当前内存 workspace；真正的持久化统一由 saveWorkspace 完成。
  void _markWorkspaceDirty(String text) {
    workspace = _snapshotWorkspace();
    dirty = true;
    busy = false;
    failed = false;
    message = text;
  }

  /// 保存帧级连招只更新内存，不调用旧模块 RPC，也不重读后端。
  Future<void> saveFrameSwitches() async {
    if (frameEdits.isEmpty) return;
    setState(() {
      for (final entry in frameEdits.entries) {
        frameSaved[entry.key] = _encodeFrameList(entry.value);
      }
      frameSwitches = [
        for (final entry in frameSaved.entries)
          for (final item in entry.value) {'state': entry.key, ...item},
      ];
      frameEditing = false;
      frameEdits = {};
      _markWorkspaceDirty('帧级连招已更新到当前内存工作区；点击“保存方案”提交暂存。');
    });
  }

  /// 清掉这把武器的全部帧级连招定制，动作块回到原样。
  /// 破坏性操作（清空定制）前的二次确认，返回 true 才继续。
  Future<bool> confirmDestructive(String title, String detail) async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    final ok = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: Text(title),
        content: Text(detail),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('确定清除'),
          ),
        ],
      ),
    );
    return ok == true && _sameWeaponContext(generation, id, profile, hash);
  }

  Future<void> clearFrameSwitches() async {
    if (!await confirmDestructive(
      '清除帧级连招定制？',
      '这会删掉该武器已保存的帧级连招编辑，动作块回到原样。此操作不可撤销（会留一份快照备份）。',
    )) {
      return;
    }
    setState(() {
      frameSaved = {};
      frameSwitches = [];
      frameEdits = {};
      frameEditing = false;
      _markWorkspaceDirty('帧级连招定制已在当前内存工作区清除；点击“保存方案”提交暂存。');
    });
  }

  List<Map<String, dynamic>> _encodeFrameList(
    List<Map<String, dynamic>> list,
  ) => [
    for (final item in list)
      {
        'attrs': [
          for (final attr in (item['attrs'] as List? ?? []))
            {'key': '${(attr as Map)['key']}', 'value': '${attr['value']}'},
        ],
      },
  ];

  Future<void> saveChain() async {
    setState(() {
      comboChain = [
        for (final e in chainDraft)
          {'old': e['old']!, 'new': e['new']!, 'key': e['key']!},
      ];
      chainDraft = [];
      chainEditing = false;
      _markWorkspaceDirty('连招链已更新到当前内存工作区；点击“保存方案”提交暂存。');
    });
  }

  Future<void> clearChain() async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
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
    if (confirmed != true || !_sameWeaponContext(generation, id, profile, hash)) return;
    setState(() {
      comboChain = [];
      chainDraft = [];
      chainEditing = false;
      _markWorkspaceDirty('连招链定制已在当前内存工作区清除；点击“保存方案”提交暂存。');
    });
  }

  /// 读取某把武器在 comborule.xml 里的限制。只在选中武器时调用：
  /// 选项表（本武器动作块真正用到的被动编号）只有服务端解得出来。
  Future<void> refreshComboRule(
    dynamic weaponId, {
    Map<String, dynamic>? snapshot,
  }) async {
    final generation = _selectionGeneration;
    final profile = activeProfile;
    final hash = sourceHash;
    try {
      final result =
          snapshot ??
          Map<String, dynamic>.from(
            await widget.api({
              'operation': 'weapon_combo_rule',
              'weapon': weaponId,
              ..._profileContextFor(profile, hash),
            }),
          );
      if (!_sameWeaponContext(generation, weaponId, profile, hash)) return;
      _validateProfileResponse(result, profile, hash);
      setState(() {
        comboRuleInfo = result;
        comboRuleFailure = '';
        comboRuleEditing = false;
        syncComboRuleDraft();
      });
    } catch (error) {
      // 读不出来时不再静默隐藏：把原因显示在卡片上。否则「后端是旧二进制」
      // 和「这把武器本来就没有限制」在界面上完全一样，只能靠猜。
      if (_sameWeaponContext(generation, weaponId, profile, hash)) {
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

  /// level 展示：整数值不带小数点（2），小数原样（0.25）。
  String _levelText(dynamic value) {
    if (value == null) return '1';
    final n = (value as num).toDouble();
    return n == n.roundToDouble() ? '${n.round()}' : '$n';
  }

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

  /// 保存（或清空）连招限制到当前内存 workspace。
  Future<void> saveComboRule({bool clear = false}) async {
    setState(() {
      if (clear) {
        comboRuleInfo = {
          ...comboRuleInfo,
          'rules': const {'max': [], 'black': [], 'white': []},
          'overridden': true,
        };
      } else {
        comboRuleInfo = {
          ...comboRuleInfo,
          'rules': {
            'max': [
              for (final e in comboRuleMaxDraft) Map<String, dynamic>.from(e),
            ],
            'black': [
              for (final e in comboRuleBlackDraft) Map<String, dynamic>.from(e),
            ],
            'white': [
              for (final e in comboRuleWhiteDraft) Map<String, dynamic>.from(e),
            ],
          },
          'overridden': true,
        };
      }
      // Draft 必须反映最新的已提交内存值；取消中的 draft 不能随 workspace
      // 一起被后端当作待应用规则覆盖 combo_rule_info。
      syncComboRuleDraft();
      comboRuleEditing = false;
      _markWorkspaceDirty(
        clear
            ? '连招限制已在当前内存工作区清除；点击“保存方案”提交暂存。'
            : '连招限制已更新到当前内存工作区；点击“保存方案”提交暂存。',
      );
    });
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
    final editable = weapon != null && canEdit(weapon!['id']);
    if (comboChain.isEmpty &&
        frameSwitches.isEmpty &&
        !chainEditing &&
        !editable) {
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
    final editable = weapon != null && canEdit(weapon!['id']);
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
                '本客户端已内置这把武器的连招限制（官方数据）。点上方「启用编辑」后可修改；'
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
              onAdd: () => setState(
                () => comboRuleMaxDraft.add({
                  'skill': '',
                  'max_combo': '1',
                  'exceed_state': '',
                  'exceed_skill_pro_id': '',
                }),
              ),
              rows: [
                if (editing)
                  for (var i = 0; i < maxRows.length; i++)
                    comboRuleMaxRow(i, maxRows[i], skillItems),
                if (!editing)
                  for (final e in maxRows)
                    comboRuleBullet(comboRuleMaxSummary(e)),
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
      child: Text(text, style: TextStyle(fontSize: 12, color: tone.shade900)),
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
                icon: const Icon(
                  Icons.close,
                  size: 16,
                  color: Colors.deepOrange,
                ),
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
              initialValue: cur.isEmpty ? (kind == 'white' ? '' : null) : cur,
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
                Icon(
                  Icons.warning_amber,
                  size: 16,
                  color: Colors.deepOrange.shade700,
                ),
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
                      horizontal: 8,
                      vertical: 3,
                    ),
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
                Icon(
                  Icons.animation,
                  size: 16,
                  color: Colors.blueGrey.shade700,
                ),
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

  /// 该武器有招架（或被定制过）的状态，按状态号排序。
  List<String> get counterStates {
    final states = <String>{
      ...counterEdits.keys,
      ...counterSaved.keys,
      for (final c in counters) '${c['state']}',
    }.toList();
    states.sort(
      (a, b) => (int.tryParse(a) ?? 0).compareTo(int.tryParse(b) ?? 0),
    );
    return states;
  }

  /// 某状态当前要展示的招架：改过用改动（null = 已标记删除），否则用渲染后的现状。
  Map<String, dynamic>? counterFor(String state) {
    if (counterEdits.containsKey(state)) return counterEdits[state];
    for (final c in counters) {
      if ('${c['state']}' == state) return Map<String, dynamic>.from(c);
    }
    return null;
  }

  bool counterTouched(String state) => counterEdits.containsKey(state);

  /// 一条招架的可读描述。
  String counterLineText(String state) {
    final edit = counterFor(state);
    if (edit == null) return '（保存后这个状态没有招架）';
    final attrs = counterAttrMap(edit);
    final box = counterBoxMap(edit);
    final size = [
      'length',
      'width',
      'heigth',
    ].map((k) => box[k] ?? '?').join('×');
    final half = attrs['anglehalfrange'] ?? '?';
    final offset = attrs['angleoffset'] ?? '0';
    return '第 ${attrs['startframe'] ?? '?'}-${attrs['endframe'] ?? '?'} 帧被攻击 → '
        '${attrs['nextstate'] ?? '?'}（±$half°，朝向 $offset° · 判定盒 $size）';
  }

  static Map<String, String> counterAttrMap(Map<String, dynamic> edit) => {
    for (final a in (edit['attrs'] as List? ?? []))
      '${(a as Map)['key']}': '${a['value']}',
  };

  static Map<String, String> counterBoxMap(Map<String, dynamic> edit) => {
    for (final a in (edit['box'] as List? ?? []))
      '${(a as Map)['key']}': '${a['value']}',
  };

  static Map<String, dynamic> encodeCounter(Map<String, dynamic> edit) => {
    'attrs': [
      for (final a in (edit['attrs'] as List? ?? []))
        {'key': '${(a as Map)['key']}', 'value': '${a['value']}'},
    ],
    'box': [
      for (final a in (edit['box'] as List? ?? []))
        {'key': '${(a as Map)['key']}', 'value': '${a['value']}'},
    ],
  };

  void startCounterEdit() {
    setState(() {
      counterEdits = {};
      counterEditing = true;
    });
  }

  void cancelCounterEdit() {
    setState(() {
      counterEdits = {};
      counterEditing = false;
    });
  }

  Future<void> counterEditState(String state) async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    final result = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _CounterDialog(
        state: state,
        states: [for (final s in (data?['states'] as List? ?? [])) '$s'],
        initial: counterFor(state),
      ),
    );
    if (result == null || !_sameWeaponContext(generation, id, profile, hash)) return;
    setState(() => counterEdits[state] = result.isEmpty ? null : result);
  }

  /// 保存招架只更新内存 workspace，不调用旧模块 RPC。
  Future<void> saveCounters() async {
    setState(() {
      for (final entry in counterEdits.entries) {
        counterSaved[entry.key] = entry.value == null
            ? null
            : Map<String, dynamic>.from(entry.value!);
      }
      counters = [
        for (final entry in counterSaved.entries)
          if (entry.value != null)
            {'state': entry.key, ...encodeCounter(entry.value!)},
      ];
      counterEditing = false;
      counterEdits = {};
      _markWorkspaceDirty('招架已更新到当前内存工作区；点击“保存方案”提交暂存。');
    });
  }

  /// 清掉这把武器的全部招架定制，动作块回到原样。
  Future<void> clearCounters() async {
    if (!await confirmDestructive(
      '清除招架定制？',
      '这会删掉该武器已保存的招架编辑，动作块回到原样。此操作不可撤销（会留一份快照备份）。',
    )) {
      return;
    }
    setState(() {
      counterSaved = {};
      counters = [];
      counterEdits = {};
      counterEditing = false;
      _markWorkspaceDirty('招架定制已在当前内存工作区清除；点击“保存方案”提交暂存。');
    });
  }

  /// 招架编辑卡：第三条连招通道 —— **被对手攻击触发**（不是按键）。
  /// 未编辑的状态沿用动作块里原有的招架（多半是从供体块带过来的）。
  Widget counterCard() {
    final states = counterStates;
    final hasEdit = counterSaved.isNotEmpty;
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
                Icon(
                  Icons.shield_outlined,
                  size: 16,
                  color: Colors.deepOrange.shade700,
                ),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    counterEditing
                        ? '招架（被攻击触发） · 编辑中'
                              '${counterEdits.isEmpty ? '' : '（改过 ${counterEdits.length} 个状态）'}'
                        : '招架（被攻击触发） · ${states.length} 个状态',
                    style: TextStyle(
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                      color: Colors.deepOrange.shade900,
                    ),
                  ),
                ),
                if (counterEditing) ...[
                  TextButton(
                    onPressed: busy ? null : cancelCounterEdit,
                    child: const Text('取消'),
                  ),
                  FilledButton.icon(
                    onPressed: busy ? null : saveCounters,
                    icon: const Icon(Icons.check, size: 16),
                    label: const Text('保存招架'),
                  ),
                ] else
                  TextButton.icon(
                    onPressed: busy ? null : startCounterEdit,
                    icon: const Icon(Icons.edit, size: 15),
                    label: Text(states.isEmpty ? '添加' : '编辑'),
                  ),
              ],
            ),
            if (states.isEmpty) ...[
              const SizedBox(height: 2),
              Text(
                counterEditing
                    ? '点下面「给某个状态添加招架」开始：在第几帧到第几帧之间被对手打中 → 跳到哪个状态。'
                    : '动作块里没有招架。招架是"被对手攻击触发"的转移，和按键连招是两套。',
                style: const TextStyle(fontSize: 12, color: Colors.black54),
              ),
            ],
            for (final state in states) counterStateGroup(state),
            if (counterEditing) ...[
              const SizedBox(height: 2),
              TextButton.icon(
                onPressed: busy ? null : counterPickStateAdd,
                icon: const Icon(Icons.add, size: 16),
                label: const Text('给某个状态添加招架', style: TextStyle(fontSize: 12)),
              ),
            ],
            if (!counterEditing && hasEdit)
              TextButton(
                onPressed: busy ? null : clearCounters,
                child: const Text(
                  '清除本武器的招架定制（动作块回到原样）',
                  style: TextStyle(fontSize: 12),
                ),
              ),
          ],
        ),
      ),
    );
  }

  Widget counterStateGroup(String state) {
    final touched = counterTouched(state);
    final empty = counterFor(state) == null;
    return Padding(
      padding: const EdgeInsets.only(left: 4, top: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            chainStateLabel(state),
            style: TextStyle(
              fontFamily: 'monospace',
              fontSize: 12,
              fontWeight: FontWeight.w600,
              color: Colors.deepOrange.shade900,
            ),
          ),
          if (touched) ...[
            const SizedBox(width: 6),
            const Text(
              '已改动',
              style: TextStyle(fontSize: 11, color: Colors.teal),
            ),
          ],
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              counterLineText(state),
              style: TextStyle(
                fontFamily: 'monospace',
                fontSize: 12,
                color: empty
                    ? Colors.deepOrange.shade700
                    : Colors.brown.shade800,
              ),
            ),
          ),
          if (counterEditing) ...[
            GestureDetector(
              onTap: busy
                  ? null
                  : () => setState(() => counterEdits[state] = null),
              child: const Padding(
                padding: EdgeInsets.only(left: 6),
                child: Icon(Icons.close, size: 14, color: Colors.deepOrange),
              ),
            ),
            GestureDetector(
              onTap: busy ? null : () => counterEditState(state),
              child: const Padding(
                padding: EdgeInsets.only(left: 8, right: 4),
                child: Icon(Icons.edit, size: 14, color: Colors.blueGrey),
              ),
            ),
          ],
        ],
      ),
    );
  }

  /// 给还没有招架的状态添加：先选状态。
  Future<void> counterPickStateAdd() async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    final all = [for (final s in (data?['states'] as List? ?? [])) '$s'];
    if (all.isEmpty) return;
    final picked = await showDialog<String>(
      context: context,
      builder: (_) => _SimplePickDialog(
        title: '为哪个状态添加招架',
        hint: '搜索状态号',
        options: [
          for (final s in all) {'value': s, 'label': chainStateLabel(s)},
        ],
      ),
    );
    if (picked == null || !_sameWeaponContext(generation, id, profile, hash)) return;
    await counterEditState(picked);
  }

  /// 解码后端的 block_elements_saved：状态 → 元素标签 → 条目。
  static Map<String, Map<String, List<Map<String, dynamic>>>>
  _decodeBlockElements(Map raw) {
    final out = <String, Map<String, List<Map<String, dynamic>>>>{};
    raw.forEach((state, perTag) {
      final tags = <String, List<Map<String, dynamic>>>{};
      ((perTag as Map?) ?? const {}).forEach((tag, list) {
        tags['$tag'] = [
          for (final e in (list as List? ?? []))
            {
              'tag': '$tag',
              'attrs': [
                for (final a in ((e as Map)['attrs'] as List? ?? []))
                  {'key': '${(a as Map)['key']}', 'value': '${a['value']}'},
              ],
              'box': [
                for (final a in ((e as Map)['box'] as List? ?? []))
                  {'key': '${(a as Map)['key']}', 'value': '${a['value']}'},
              ],
            },
        ];
      });
      out['$state'] = tags;
    });
    return out;
  }

  // -------------------------------------------------------------------------
  // 动作分支（按状态切换招式形态）
  // -------------------------------------------------------------------------

  /// 一份动作段的归一化
  /// （name/start/end[/damage/skillproid/replay_times/anm_id/buff]）。
  ///
  /// `skillproid` / `replay_times` / `anm_id` 都必须原样留住并在保存时回发：
  /// 后端靠它们判断「这段还是原来那段、号不变」，丢掉任何一个都会让保存变成
  /// 「重建」——每存一次就换一批新命中编号（旧号成孤儿），卡帧也会被抹掉。
  static dynamic _normalizeDamage(dynamic value) {
    if (value is num) return value;
    final text = '$value'.trim();
    final parsed = num.tryParse(text);
    return parsed ?? value;
  }

  static List<Map<String, dynamic>> _decodeSegments(List? list) => [
    for (final s in (list ?? const []))
      {
        'name': '${(s as Map)['name'] ?? ''}',
        'start': int.tryParse('${s['start'] ?? 0}') ?? 0,
        'end': int.tryParse('${s['end'] ?? 0}') ?? 0,
        if (s['damage'] != null) 'damage': _normalizeDamage(s['damage']),
        if (s['skillproid'] != null) 'skillproid': '${s['skillproid']}',
        if (s['template_skillproid'] != null)
          'template_skillproid': '${s['template_skillproid']}',
        if (s['replay_times'] != null)
          'replay_times': int.tryParse('${s['replay_times']}') ?? 0,
        if (s['anm_id'] != null) 'anm_id': '${s['anm_id']}',
        if (s['buff'] != null) 'buff': '${s['buff']}',
      },
  ];

  /// 解码后端的 variants / variants_saved：状态号 → 分支列表。
  /// 分支：condition（触发状态号）+ segments（段：name/start/end[/damage/skillproid/buff]）。
  static Map<String, List<Map<String, dynamic>>> _decodeVariants(Map raw) {
    final out = <String, List<Map<String, dynamic>>>{};
    raw.forEach((state, list) {
      out['$state'] = [
        for (final e in (list as List? ?? []))
          {
            'condition': int.parse('${(e as Map)['condition']}'),
            'remove': (e['remove'] as bool?) ?? false,
            'segments': _decodeSegments(e['segments'] as List?),
          },
      ];
    });
    return out;
  }

  /// 解码 variant_bases：状态号 → 无条件块的动作段。
  static Map<String, List<Map<String, dynamic>>> _decodeBases(Map raw) {
    final out = <String, List<Map<String, dynamic>>>{};
    raw.forEach((state, list) {
      out['$state'] = _decodeSegments(list as List?);
    });
    return out;
  }

  /// 有动作分支（或本次改过）的状态号，按数字排序。
  List<String> get variantStates {
    final states = <String>{
      ...variantsEdit.keys,
      ...variants.keys,
      ...variantsSaved.keys,
    }.toList();
    states.sort(
      (a, b) => (int.tryParse(a) ?? 0).compareTo(int.tryParse(b) ?? 0),
    );
    return states;
  }

  /// 某状态本次编辑里「要删掉」的 condition 集合。
  Set<String> variantRemovedConditions(String state) => {
    for (final source in [
      variants[state] ?? const <Map<String, dynamic>>[],
      variantsSaved[state] ?? const <Map<String, dynamic>>[],
      variantsEdit[state] ?? const <Map<String, dynamic>>[],
    ])
      for (final v in source)
        if ((v['remove'] as bool?) == true) '${v['condition']}',
  };

  /// 某状态本次编辑里「新增/覆盖」的分支。
  List<Map<String, dynamic>> variantPendingFor(String state) => [
    for (final v in variantsEdit[state] ?? const [])
      if ((v['remove'] as bool?) != true) v,
  ];

  /// 编辑器里要显示的分支行：合并「渲染现状」与「本次编辑」，同 condition 以本次编辑为准。
  /// origin：current=块里已有，edit=改过，new=本次新增。
  List<Map<String, dynamic>> variantRowsFor(String state) {
    final removed = variantRemovedConditions(state);
    final edits = <String, Map<String, dynamic>>{
      for (final v in variantPendingFor(state)) '${v['condition']}': v,
    };
    final rows = <Map<String, dynamic>>[];
    final seen = <String>{};
    for (final v in variants[state] ?? const []) {
      final cond = '${v['condition']}';
      if (removed.contains(cond)) continue;
      final replaced = edits[cond];
      rows.add({
        'condition': cond,
        'segments': replaced != null ? replaced['segments'] : v['segments'],
        'origin': replaced != null ? 'edit' : 'current',
      });
      seen.add(cond);
    }
    for (final v in variantsSaved[state] ?? const <Map<String, dynamic>>[]) {
      final cond = '${v['condition']}';
      if (removed.contains(cond) ||
          seen.contains(cond) ||
          v['remove'] == true) {
        continue;
      }
      final replaced = edits[cond];
      rows.add({
        'condition': cond,
        'segments': replaced != null ? replaced['segments'] : v['segments'],
        'origin': replaced != null ? 'edit' : 'saved',
      });
      seen.add(cond);
    }
    for (final v in variantPendingFor(state)) {
      final cond = '${v['condition']}';
      if (seen.contains(cond)) continue;
      rows.add({'condition': cond, 'segments': v['segments'], 'origin': 'new'});
    }
    return rows;
  }

  /// 一个状态当前**仍被引用**的命中属性号：无条件块 + 所有分支段（含本次编辑）。
  /// 分支里删掉一段、或换掉某段的命中属性后，旧号的数值不该继续挂在规则里。
  Set<String> referencedHitIDs(String state) {
    final ids = <String>{};
    for (final s in variantBaseSegments(state)) {
      final id = '${(s as Map)['skillproid'] ?? ''}'.trim();
      if (id.isNotEmpty) ids.add(id);
    }
    for (final row in variantRowsFor(state)) {
      for (final s in (row['segments'] as List? ?? const [])) {
        final id = '${(s as Map)['skillproid'] ?? ''}'.trim();
        if (id.isNotEmpty) ids.add(id);
      }
    }
    return ids;
  }

  /// 动作分支对话框需要的联动上下文（命中属性明细 / 当前数值覆盖 / 已进包的号）。
  _VariantHitContext variantHitContext(String state) {
    final stages = weapon?['stages'] as List? ?? const [];
    final index = stages.indexWhere((s) => '${(s as Map)['state']}' == state);
    if (index < 0) {
      return _VariantHitContext(
        const <String, Map<String, dynamic>>{},
        const <String, dynamic>{},
        const <String>{},
        {...variantOccupiedIDs, ...localVariantIDs},
        {...localVariantIDs},
        variantSkillProMin,
        variantSkillProMax,
      );
    }
    final stage = Map<String, dynamic>.from(stages[index] as Map);
    final rule = index < rules.length ? rules[index] : null;
    final ruleProperties = rule?['properties'] as Map? ?? const {};
    final hits = <String, Map<String, dynamic>>{};
    for (final h in (stage['hits'] as List? ?? const [])) {
      final hit = Map<String, dynamic>.from(h as Map);
      final id = '${hit['id']}';
      hits[id] = {
        ...hit,
        'values': _mergeHitValues([
          hit,
          ruleProperties[id],
          hitProperties[id],
        ]),
      };
    }
    // 模板必须优先取当前状态第一段真实命中段，不能被旧分支或 map 遍历顺序改变。
    final firstStageHit = (stage['hits'] as List? ?? const []).cast<Map?>().firstWhere(
      (hit) => '${hit?['id'] ?? ''}'.trim().isNotEmpty,
      orElse: () => null,
    );
    final defaultTemplateID = '${firstStageHit?['id'] ?? ''}'.trim();
    for (final branch in variantRowsFor(state)) {
      for (final segment in (branch['segments'] as List? ?? const [])) {
        final row = segment as Map;
        final id = '${row['skillproid'] ?? ''}'.trim();
        if (id.isEmpty || hits.containsKey(id)) continue;
        final requestedTemplate =
            '${row['template_skillproid'] ?? ''}'.trim();
        final template = requestedTemplate.isNotEmpty
            ? requestedTemplate
            : defaultTemplateID;
        final source = hits[template] ??
            (template.isEmpty ? null : {
              'id': template,
              'values': _mergeHitValues([
                hitProperties[template],
                ruleProperties[template],
              ]),
            });
        final canonical = hitProperties[id];
        hits[id] = {
          ...?source,
          if (canonical is Map)
            for (final entry in canonical.entries)
              if (entry.key != 'values') '${entry.key}': entry.value,
          'id': id,
          'variant': '${branch['condition']}',
          'buff': source?['buff'] ?? '0',
          'values': _mergeHitValues([
            source,
            hitProperties[template],
            ruleProperties[template],
            canonical,
            if (row['damage'] != null) {'SkillDamage': row['damage']},
          ]),
        };
      }
    }
    return _VariantHitContext(
      hits,
      {
        for (final entry in hits.entries)
          entry.key: _hitValues(entry.value),
        for (final entry in ((rule?['properties'] as Map?) ?? const {}).entries)
          '${entry.key}': {
            ..._hitValues(hits['${entry.key}']),
            ..._ruleValues(entry.value),
          },
        for (final entry in hitProperties.entries)
          '${entry.key}': {
            ..._hitValues(hits['${entry.key}']),
            ..._hitValues(entry.value),
          },
      },
      {for (final id in (stage['property_ids'] as List? ?? const [])) '$id'},
      {...variantOccupiedIDs},
      {...localVariantIDs},
      variantSkillProMin,
      variantSkillProMax,
    );
  }

  /// 把动作分支里改过的命中属性数值写进该状态的规则，并按「仍被引用」清掉废弃号。
  ///
  /// 走的是 `rule['properties'][id]` —— 和「连招与命中效果」页里的命中属性编辑
  /// **同一份数据**，所以两处显示联动、两处生效；分支里删掉的号也在这里一起撤掉。
  void applyHitProperties(String state, Map<String, dynamic> values) {
    values.forEach((id, value) {
      if (value is Map && value.isNotEmpty) {
        final current = _hitValues(hitProperties['$id']);
        hitProperties['$id'] = {
          ...current,
          ..._hitValues(value),
        };
      }
    });
    final referenced = <String>{};
    for (final stage in (weapon?['stages'] as List? ?? const [])) {
      final state = '${stage['state']}';
      referenced.addAll(referencedHitIDs(state));
      final liveBranchIDs = referencedHitIDs(state);
      for (final id in (stage['property_ids'] as List? ?? const [])) {
        if (!isVariantSkillProID(
          '$id',
          minimum: variantSkillProMin,
          maximum: variantSkillProMax,
        )) {
          referenced.add('$id');
        }
      }
      for (final hit in (stage['hits'] as List? ?? const [])) {
        final id = '${(hit as Map)['id']}';
        if (!isVariantSkillProID(
              id,
              minimum: variantSkillProMin,
              maximum: variantSkillProMax,
            ) ||
            liveBranchIDs.contains(id)) {
          referenced.add(id);
        }
      }
    }
    hitProperties.removeWhere((id, _) => !referenced.contains(id));
    _projectHitPropertiesToRules();
    editorVersion++;
    dirty = true;
  }

  void _projectHitPropertiesToRules() {
    for (final rule in rules) {
      final properties = rule['properties'] as Map?;
      final keep = <String>{};
      final ruleStage = '${rule['stage']}';
      final stage = (weapon?['stages'] as List? ?? const []).cast<Map>().where(
        (s) => '${s['state']}' == ruleStage || '${s['stage']}' == ruleStage,
      );
      if (stage.isNotEmpty) {
        final stageRow = stage.first;
        keep.addAll(
          (stageRow['property_ids'] as List? ?? const [])
              .map((id) => '$id')
              .where((id) => !isVariantSkillProID(id)),
        );
        keep.addAll(
          (stageRow['hits'] as List? ?? const [])
              .map((hit) => '${(hit as Map)['id']}')
              .where((id) => !isVariantSkillProID(id)),
        );
      }
      // Variant segments are rendered from variants, not from the base rule.
      // Do not revive stale branch IDs from rules.properties either.
      if (properties != null) {
        keep.addAll(
          properties.keys
              .map((id) => '$id')
              .where((id) => !isVariantSkillProID(id)),
        );
      }
      final stageHits = <String, dynamic>{
        if (stage.isNotEmpty)
          for (final hit in (stage.first['hits'] as List? ?? const []))
            '${(hit as Map)['id']}': hit,
      };
      final current = <String, dynamic>{};
      for (final id in keep) {
        if (hitProperties[id] is! Map) continue;
        final values = _ruleValues(_mergeHitValues([
          stageHits[id],
          properties?[id],
          hitProperties[id],
        ]));
        final shared = hitProperties[id] as Map;
        hitProperties[id] = shared['values'] is Map
            ? {...shared, 'values': values}
            : values;
        current[id] = values;
      }
      if (current.isEmpty) {
        rule.remove('properties');
      } else {
        rule['properties'] = current;
      }
    }
  }

  void variantPut(
    String state,
    int condition,
    List<Map<String, dynamic>> segments, {
    Map<String, dynamic>? hitProperties,
  }) {
    setState(() {
      final list = [...(variantsEdit[state] ?? const <Map<String, dynamic>>[])];
      list.removeWhere((v) => '${v['condition']}' == '$condition');
      list.add({
        'condition': condition,
        'segments': segments,
        if (segments.isEmpty) 'copy_base': true,
      });
      variantsEdit[state] = list;
      syncVariantOccupiedIDs();
      if (hitProperties != null) applyHitProperties(state, hitProperties);
    });
  }

  void variantRemove(String state, String condition) {
    setState(() {
      final list = [...(variantsEdit[state] ?? const <Map<String, dynamic>>[])];
      list.removeWhere((v) => '${v['condition']}' == condition);
      final exists =
          (variants[state] ?? const []).any(
            (v) => '${v['condition']}' == condition,
          ) ||
          (variantsSaved[state] ?? const []).any(
            (v) => '${v['condition']}' == condition,
          );
      if (exists) {
        list.add({
          'condition': int.parse(condition),
          'remove': true,
          'segments': const [],
        });
      }
      variantsEdit[state] = list;
      syncVariantOccupiedIDs();
      // 分支删了，它独占的命中属性数值也一起撤掉（仍被无条件块或别的分支引用的保留）。
      applyHitProperties(state, const <String, dynamic>{});
    });
  }

  void startVariantEdit() {
    setState(() {
      variantsEdit = {};
      _variantRulesSnapshot = [
        for (final rule in rules)
          {
            ...rule,
            if (rule['properties'] is Map)
              'properties': {
                for (final entry in (rule['properties'] as Map).entries)
                  '${entry.key}': Map<String, dynamic>.from(entry.value as Map),
              },
          },
      ];
      _variantHitPropertiesSnapshot = Map<String, dynamic>.from(
        jsonDecode(jsonEncode(hitProperties)) as Map,
      );
      _variantDirtySnapshot = dirty;
      _variantOccupiedSnapshot = {
        for (final id in variantOccupiedIDs) id: true,
      };
      variantEditing = true;
    });
  }

  void cancelVariantEdit() {
    setState(() {
      variantsEdit = {};
      if (_variantRulesSnapshot != null) {
        rules = _variantRulesSnapshot!;
        dirty = _variantDirtySnapshot;
      }
      if (_variantHitPropertiesSnapshot != null) {
        hitProperties = _variantHitPropertiesSnapshot!;
      }
      if (_variantOccupiedSnapshot != null) {
        variantOccupiedIDs = _variantOccupiedSnapshot!.keys.toSet();
      }
      _variantHitPropertiesSnapshot = null;
      _variantRulesSnapshot = null;
      _variantOccupiedSnapshot = null;
      editorVersion++;
      variantEditing = false;
    });
  }

  dynamic _variantSegmentDamage(Map<String, dynamic> segment) {
    final id = '${segment['skillproid'] ?? ''}'.trim();
    if (id.isEmpty) return segment['damage'];
    final values = hitProperties[id];
    if (values is Map) {
      final canonical = values['values'];
      if (canonical is Map && canonical['SkillDamage'] != null) {
        return canonical['SkillDamage'];
      }
      if (values['SkillDamage'] != null) return values['SkillDamage'];
    }
    return segment['damage'];
  }

  /// 一条分支的可读描述。
  String variantBranchSummary(Map<String, dynamic> branch) {
    final cond = '${branch['condition'] ?? ''}';
    final segs = (branch['segments'] as List? ?? [])
        .map((s) => Map<String, dynamic>.from(s as Map))
        .toList();
    if ((branch['remove'] as bool?) == true) {
      return '删掉 ${ustateLabel(cond)} 的分支';
    }
    final head = '持有 ${ustateLabel(cond)} 时';
    if (segs.isEmpty) return '$head → 照抄本招动作段（只换命中编号）';
    // 段分三类：带命中编号的（造成伤害）、卡帧的（画面定格，用来把多段判定挤进
    // 几帧）、纯动作的（既不打人也不定格）。只报「N 段」看不出它们的差别，而
    // 这三类的多寡恰恰决定这一招打几下。
    final hits = segs
        .where((s) => '${s['skillproid'] ?? ''}'.isNotEmpty)
        .length;
    final holds = segs
        .where((s) => (int.tryParse('${s['replay_times'] ?? 0}') ?? 0) > 0)
        .length;
    final idle = segs.length - hits - holds;
    final parts = <String>['命中 $hits 段'];
    if (holds > 0) parts.add('卡帧 $holds 段');
    if (idle > 0) parts.add('纯动作 $idle 段');
    return '$head → ${segs.length} 段（${parts.join('，')}）：'
        '${segs.map((s) {
          final dmg = _variantSegmentDamage(s);
          final sp = '${s['skillproid'] ?? ''}';
          final hold = (int.tryParse('${s['replay_times'] ?? 0}') ?? 0) > 0;
          return '${s['name'] ?? '?'}[${s['start'] ?? '?'}–${s['end'] ?? '?'}]'
              '${sp.isNotEmpty ? ' 命中属性$sp' : (hold ? ' 卡帧' : '')}'
              '${dmg != null && '$dmg'.isNotEmpty ? ' 伤害$dmg' : ''}';
        }).join('，')}';
  }

  /// 给某状态添加/修改一条分支：先选状态，再编辑条件与段。
  Future<void> variantAddFor(String state) async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    final ctx = variantHitContext(state);
    final result = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _VariantBranchDialog(
        ustates: ustateOptions,
        stateLabel: chainStateLabel(state),
        initial: null,
        baseSegments: variantBaseSegments(state),
        api: _apiForWeaponContext(generation, id, profile, hash),
        hits: ctx.hits,
        hitOverrides: ctx.overrides,
        fields: (data?['fields'] as List? ?? const []),
        effects: (data?['effects'] as List? ?? const []),
        hitOptions: Map<String, dynamic>.from(
          data?['hit_options'] as Map? ?? const <String, dynamic>{},
        ),
        allowed: Map<String, dynamic>.from(
          weapon?['allowed_values'] as Map? ?? const <String, dynamic>{},
        ),
        applied: ctx.applied,
        occupied: ctx.occupied,
        localReserved: ctx.localReserved,
        minimum: ctx.minimum,
        maximum: ctx.maximum,
      ),
    );
    if (result == null || !_sameWeaponContext(generation, id, profile, hash)) return;
    final condition = int.tryParse('${result['condition']}') ?? 0;
    if (condition <= 0) return;
    variantPut(
      state,
      condition,
      (result['segments'] as List? ?? [])
          .map((s) => Map<String, dynamic>.from(s as Map))
          .toList(),
      hitProperties: Map<String, dynamic>.from(
        result['hit_properties'] as Map? ?? const <String, dynamic>{},
      ),
    );
  }

  /// 编辑已有的某条分支（current / new / edit 都走这里，先回填现值）。
  Future<void> variantEditRow(String state, Map<String, dynamic> row) async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    final ctx = variantHitContext(state);
    final result = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _VariantBranchDialog(
        ustates: ustateOptions,
        stateLabel: chainStateLabel(state),
        initial: row,
        baseSegments: variantBaseSegments(state),
        api: _apiForWeaponContext(generation, id, profile, hash),
        hits: ctx.hits,
        hitOverrides: ctx.overrides,
        fields: (data?['fields'] as List? ?? const []),
        effects: (data?['effects'] as List? ?? const []),
        hitOptions: Map<String, dynamic>.from(
          data?['hit_options'] as Map? ?? const <String, dynamic>{},
        ),
        allowed: Map<String, dynamic>.from(
          weapon?['allowed_values'] as Map? ?? const <String, dynamic>{},
        ),
        applied: ctx.applied,
        occupied: ctx.occupied,
        localReserved: ctx.localReserved,
        minimum: ctx.minimum,
        maximum: ctx.maximum,
      ),
    );
    if (result == null || !_sameWeaponContext(generation, id, profile, hash)) return;
    final condition = int.tryParse('${result['condition']}') ?? 0;
    if (condition <= 0) return;
    variantPut(
      state,
      condition,
      (result['segments'] as List? ?? [])
          .map((s) => Map<String, dynamic>.from(s as Map))
          .toList(),
      hitProperties: Map<String, dynamic>.from(
        result['hit_properties'] as Map? ?? const <String, dynamic>{},
      ),
    );
  }

  /// 某招无条件块的动作段（供「照抄」回填参考）。
  List<Map<String, dynamic>> variantBaseSegments(String state) =>
      variantBases[state] ?? const [];

  /// 挑一个状态来加分支。
  Future<void> variantPickStateAndAdd() async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    final all = [for (final s in (data?['states'] as List? ?? [])) '$s'];
    if (all.isEmpty) return;
    final picked = await showDialog<String>(
      context: context,
      builder: (_) => _SimplePickDialog(
        title: '为哪个状态添加动作分支',
        hint: '搜索状态号',
        options: [
          for (final s in all) {'value': s, 'label': chainStateLabel(s)},
        ],
      ),
    );
    if (picked == null || !_sameWeaponContext(generation, id, profile, hash)) return;
    await variantAddFor(picked);
  }

  void _applyVariantPayloadToMemory(Map<String, dynamic> payload) {
    for (final entry in payload.entries) {
      final state = entry.key;
      final current = [
        for (final value in (variants[state] ?? const []))
          Map<String, dynamic>.from(value),
      ];
      for (final value in (entry.value as List? ?? const [])) {
        final edit = Map<String, dynamic>.from(value as Map);
        final condition = '${edit['condition']}';
        current.removeWhere((row) => '${row['condition']}' == condition);
        if ((edit['remove'] as bool?) != true) {
          current.add({
            'condition': int.parse(condition),
            'segments': [
              for (final segment in (edit['segments'] as List? ?? const []))
                Map<String, dynamic>.from(segment as Map),
            ],
          });
        }
      }
      current.sort(
        (a, b) => '${a['condition']}'.compareTo('${b['condition']}'),
      );
      // Keep deletion tombstones in the workspace snapshot.  The live rows are
      // removed from the editor, but dropping the marker here would make the
      // next "save plan/apply" indistinguishable from "no change".
      final tombstones = [
        for (final edit in (entry.value as List? ?? const []))
          if ((edit as Map)['remove'] == true)
            {
              'condition': int.parse('${edit['condition']}'),
              'remove': true,
              'segments': const <Map<String, dynamic>>[],
            },
      ];
      variants[state] = [
        ...current,
        ...tombstones,
      ];
      variantsSaved[state] = [
        for (final row in variants[state]!) Map<String, dynamic>.from(row),
      ];
    }
  }

  /// 阶段 2：保存动作分支只提交到当前 WeaponWorkspace，不触发正式 RPC。
  dynamic _normalizeVariantDamage(dynamic value) {
    if (value is num) return value;
    return num.tryParse('$value'.trim()) ?? value;
  }

  Future<void> saveVariants() async {
    if (variantsEdit.isEmpty) {
      setState(() => message = '没有改动，无需保存');
      return;
    }
    final payload = <String, dynamic>{};
    variantsEdit.forEach((state, edits) {
      final list = <Map<String, dynamic>>[];
      for (final e in edits) {
        final cond = int.tryParse('${e['condition']}') ?? 0;
        if (cond <= 0) continue;
        if ((e['remove'] as bool?) == true) {
          list.add({'condition': cond, 'remove': true});
          continue;
        }
        list.add({
          'condition': cond,
          'segments': [
            for (final s in (e['segments'] as List? ?? []))
              {
                'name': '${(s as Map)['name']}',
                'start': s['start'],
                'end': s['end'],
                if (_hitValues(variantHitContext(state)
                            .overrides['${s['skillproid']}'])['SkillDamage'] !=
                        null ||
                    s['damage'] != null)
                  'damage': _normalizeVariantDamage(
                    _hitValues(variantHitContext(state)
                            .overrides['${s['skillproid']}'])['SkillDamage'] ??
                        s['damage'],
                  ),
                // 这三个是「原位编辑」的凭据：后端拿 skillproid 判这一段是不是
                // 上一轮分的号（是就原样保留、不换号），拿 anm_id 保住片断编号，
                // 拿 replay_times 保住卡帧。不回发 = 每次保存都重铸一批新号。
                if ('${s['skillproid'] ?? ''}'.isNotEmpty)
                  'skillproid': '${s['skillproid']}',
                if ('${s['template_skillproid'] ?? ''}'.isNotEmpty)
                  'template_skillproid': '${s['template_skillproid']}',
                if ('${s['skillproid'] ?? ''}'.trim().isNotEmpty ||
                    (int.tryParse('${s['replay_times'] ?? 0}') ?? 0) > 0)
                  'replay_times':
                      int.tryParse('${s['replay_times'] ?? 0}') ?? 0,
                if ('${s['anm_id'] ?? ''}'.isNotEmpty)
                  'anm_id': '${s['anm_id']}',
              },
          ],
        });
      }
      if (list.isNotEmpty) payload[state] = list;
    });
    if (payload.isEmpty) {
      setState(() => message = '没有有效的分支改动');
      return;
    }
    if (!mounted) return;
    setState(() {
      _applyVariantPayloadToMemory(payload);
      for (final state in payload.keys) {
        final stages = List<dynamic>.from(weapon?['stages'] as List? ?? const []);
        final index = stages.indexWhere((row) => '${row['state']}' == state);
        if (index < 0) continue;
        final stage = Map<String, dynamic>.from(stages[index] as Map);
        final context = variantHitContext(state);
        final ids = referencedHitIDs(state);
        final hits = <String, Map<String, dynamic>>{
          for (final hit in (stage['hits'] as List? ?? const []))
            '${(hit as Map)['id']}': {
              ...Map<String, dynamic>.from(hit),
              if (hitProperties['${hit['id']}'] is Map)
                'values': {
                  ..._hitValues(hit),
                  ..._hitValues(hitProperties['${hit['id']}']),
                },
            },
        };
        for (final id in ids) {
          final source = context.hits[id];
          if (source == null) continue;
          hits[id] = {
            ...source,
            'id': id,
            'values': {
              ..._hitValues(source),
              ..._hitValues(hitProperties[id]),
            },
          };
        }
        stage['hits'] = hits.values.toList();
        stage['property_ids'] = ids.toList();
        stages[index] = stage;
        weapon = {...weapon!, 'stages': stages};
      }
      _projectHitPropertiesToRules();
      variantEditing = false;
      variantsEdit = {};
      _variantRulesSnapshot = null;
      _variantOccupiedSnapshot = null;
      dirty = true;
      busy = false;
      failed = false;
      message = '动作分支已写入当前武器内存；点击“保存方案”后才会生成暂存副本。';
      workspace = _snapshotWorkspace();
    });
  }

  Future<void> clearVariants() async {
    if (!await confirmDestructive(
      '清除动作分支定制？',
      '这会删掉该武器已保存的「按状态切换招式形态」编辑，动作块回到原样。此操作不可撤销（会留一份快照备份）。',
    )) {
      return;
    }
    if (!mounted) return;
    setState(() {
      variants = {};
      variantsSaved = {};
      variantsEdit = {};
      variantOccupiedIDs = {...variantServerOccupiedIDs};
      variantEditing = false;
      _variantRulesSnapshot = null;
      _variantOccupiedSnapshot = null;
      dirty = true;
      failed = false;
      busy = false;
      message = '动作分支已在当前武器内存中清除；点击“保存方案”后才会生成暂存副本。';
      workspace = _snapshotWorkspace();
    });
  }

  /// 「动作分支」编辑卡：同一招在持有某状态时改成另一套动作段。
  Widget variantCard() {
    final states = variantStates;
    final hasEdit = variantsSaved.isNotEmpty || variants.isNotEmpty;
    return Padding(
      padding: const EdgeInsets.only(top: 6),
      child: Container(
        key: const ValueKey('weapon-variants'),
        width: double.infinity,
        padding: const EdgeInsets.all(8),
        decoration: BoxDecoration(
          color: Colors.amber.shade50,
          borderRadius: BorderRadius.circular(6),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.call_split, size: 16, color: Colors.amber.shade800),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    variantEditing
                        ? '动作分支 · 编辑中'
                              '${variantsEdit.isEmpty ? '' : '（改过 ${variantsEdit.length} 个状态）'}'
                        : '动作分支 · ${states.length} 个状态',
                    style: TextStyle(
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                      color: Colors.amber.shade900,
                    ),
                  ),
                ),
                if (variantEditing) ...[
                  TextButton(
                    onPressed: busy ? null : cancelVariantEdit,
                    child: const Text('取消'),
                  ),
                  FilledButton.icon(
                    onPressed: busy ? null : saveVariants,
                    icon: const Icon(Icons.check, size: 16),
                    label: const Text('保存'),
                  ),
                ] else
                  TextButton.icon(
                    onPressed: busy ? null : startVariantEdit,
                    icon: const Icon(Icons.edit, size: 15),
                    label: Text(states.isEmpty ? '添加' : '编辑'),
                  ),
              ],
            ),
            if (states.isEmpty) ...[
              const SizedBox(height: 2),
              Text(
                variantEditing
                    ? '点下面「给某个状态添加」：该状态生效时这一招换成另一套动作段。'
                    : '没有按状态切换形态的招式。持有指定状态时，同一招可换成另一套'
                          '动作段（多打几下 / 改伤害 / 挂别的 Buff）。',
                style: const TextStyle(fontSize: 12, color: Colors.black54),
              ),
            ],
            for (final state in states) variantStateGroup(state),
            if (variantEditing) ...[
              const SizedBox(height: 2),
              TextButton.icon(
                onPressed: busy ? null : variantPickStateAndAdd,
                icon: const Icon(Icons.add, size: 16),
                label: const Text('给某个状态添加', style: TextStyle(fontSize: 12)),
              ),
            ],
            if (!variantEditing && hasEdit)
              TextButton(
                onPressed: busy ? null : clearVariants,
                child: const Text(
                  '清除本武器的动作分支定制（动作块回到原样）',
                  style: TextStyle(fontSize: 12),
                ),
              ),
          ],
        ),
      ),
    );
  }

  Widget variantStateGroup(String state) {
    final rows = variantRowsFor(state);
    return Padding(
      padding: const EdgeInsets.only(top: 4),
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
                  color: Colors.amber.shade900,
                ),
              ),
              if (variantEditing) ...[
                const Spacer(),
                GestureDetector(
                  onTap: busy ? null : () => variantAddFor(state),
                  child: const Padding(
                    padding: EdgeInsets.only(right: 2),
                    child: Icon(
                      Icons.add_circle_outline,
                      size: 15,
                      color: Colors.teal,
                    ),
                  ),
                ),
              ],
            ],
          ),
          if (rows.isEmpty)
            const Padding(
              padding: EdgeInsets.only(left: 10, top: 2),
              child: Text(
                '（保存后这个状态不再有分支）',
                style: TextStyle(fontSize: 11, color: Colors.deepOrange),
              ),
            ),
          for (final row in rows)
            Padding(
              padding: const EdgeInsets.only(left: 10, top: 1),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Expanded(
                    child: Text(
                      variantBranchSummary(row),
                      style: TextStyle(
                        fontFamily: 'monospace',
                        fontSize: 11,
                        color: row['origin'] == 'current'
                            ? Colors.brown.shade800
                            : Colors.amber.shade900,
                      ),
                    ),
                  ),
                  if (variantEditing) ...[
                    GestureDetector(
                      onTap: busy
                          ? null
                          : () => variantRemove(state, '${row['condition']}'),
                      child: const Padding(
                        padding: EdgeInsets.only(left: 6),
                        child: Icon(
                          Icons.close,
                          size: 13,
                          color: Colors.deepOrange,
                        ),
                      ),
                    ),
                    GestureDetector(
                      onTap: busy ? null : () => variantEditRow(state, row),
                      child: const Padding(
                        padding: EdgeInsets.only(left: 8, right: 2),
                        child: Icon(
                          Icons.edit,
                          size: 13,
                          color: Colors.blueGrey,
                        ),
                      ),
                    ),
                  ],
                ],
              ),
            ),
        ],
      ),
    );
  }

  List<String> get blockElementStates {
    final states = <String>{
      ...blockElementsEdit.keys,
      ...blockElementsSaved.keys,
      for (final e in blockElements) '${e['state']}',
    }.toList();
    states.sort(
      (a, b) => (int.tryParse(a) ?? 0).compareTo(int.tryParse(b) ?? 0),
    );
    return states;
  }

  /// 某状态要展示的元素标签（改过的、保存过的、块里原有的）。
  List<String> blockTagsOf(String state) {
    final tags = <String>{
      ...?blockElementsEdit[state]?.keys,
      ...?blockElementsSaved[state]?.keys,
      for (final e in blockElements)
        if ('${e['state']}' == state)
          for (final el in (e['elements'] as List? ?? []))
            '${(el as Map)['tag']}',
    }.toList();
    tags.sort((a, b) => blockElementOrder(a).compareTo(blockElementOrder(b)));
    return tags;
  }

  int blockElementOrder(String tag) {
    // 按后端声明的顺序排（同一组里的多个元素以前会撞成同一个序号，顺序不稳定）。
    var index = 0;
    for (final g in blockElementGroups) {
      for (final e in (g['elements'] as List? ?? [])) {
        if ('${(e as Map)['tag']}' == tag) return index;
        index++;
      }
    }
    return 9999;
  }

  /// 某 (状态,标签) 当前要展示的条目：改过用改动，否则用已保存的，再看块里的现状。
  List<Map<String, dynamic>> blockListFor(String state, String tag) {
    final edit = blockElementsEdit[state];
    if (edit != null && edit.containsKey(tag)) return edit[tag]!;
    final saved = blockElementsSaved[state];
    if (saved != null && saved.containsKey(tag)) return saved[tag]!;
    return [
      for (final e in blockElements)
        if ('${e['state']}' == state)
          for (final el in (e['elements'] as List? ?? []))
            if ('${(el as Map)['tag']}' == tag)
              {
                'tag': tag,
                'attrs': [
                  for (final a in ((el as Map)['attrs'] as List? ?? []))
                    {'key': '${(a as Map)['key']}', 'value': '${a['value']}'},
                ],
                'box': [
                  for (final a in ((el as Map)['box'] as List? ?? []))
                    {'key': '${(a as Map)['key']}', 'value': '${a['value']}'},
                ],
              },
    ];
  }

  bool blockTouched(String state, String tag) =>
      blockElementsEdit[state]?.containsKey(tag) ?? false;

  void blockSet(String state, String tag, List<Map<String, dynamic>> list) {
    setState(() {
      blockElementsEdit.putIfAbsent(state, () => {})[tag] = list;
    });
  }

  void startBlockElementEdit() {
    setState(() {
      blockElementsEdit = {};
      blockElementEditing = true;
    });
  }

  void cancelBlockElementEdit() {
    setState(() {
      blockElementsEdit = {};
      blockElementEditing = false;
    });
  }

  String blockElementTagLabel(String tag) {
    for (final g in blockElementGroups) {
      for (final e in (g['elements'] as List? ?? [])) {
        if ('${(e as Map)['tag']}' == tag) return '${e['label']}';
      }
    }
    return tag;
  }

  /// 状态编号 → 「12（复活）」，没收录就只显示编号。
  String ustateLabel(String id) {
    for (final u in ustateOptions) {
      if (u['id'] == id && (u['name'] ?? '').isNotEmpty) {
        return '$id（${u['name']}）';
      }
    }
    return id;
  }

  String blockElementLine(String tag, List<Map<String, dynamic>> attrsList) {
    final attrs = <String, String>{
      for (final a in attrsList) '${a['key']}': '${a['value']}',
    };
    switch (tag) {
      case 'FakeUnAttack':
      case 'BodyGraze':
        return '第 ${attrs['startframe'] ?? '?'}–${attrs['endframe'] ?? '?'} 帧';
      case 'DirectionalInvc':
        return '第 ${attrs['startframe'] ?? '?'}–${attrs['endframe'] ?? '?'} 帧'
            '（±${attrs['anglehalfrange'] ?? '?'}°，朝向 ${attrs['angleoffset'] ?? '0'}°）';
      case 'UState':
        // 只在 <LockedAttackHit>（命中判定点）里：命中这一击时给被打中的人挂状态。
        return '命中 → 状态 ${ustateLabel(attrs['id'] ?? '')}'
            ' · 等级 ${attrs['level'] ?? '-'} · ${attrs['duration'] ?? '-'} ms';
      case 'Ustate':
        // 只在 <Condition> 里：这是「拥有该状态时这段动作才成立」的判断，不是施加。
        return '需拥有 ${ustateLabel(attrs['id'] ?? '')}';
      case 'AddBuff':
        return '第 ${attrs['frame'] ?? '0'} 帧 → 状态 ${ustateLabel(attrs['UnNormalState'] ?? '')}'
            ' · 等级 ${attrs['UStateLevel'] ?? '-'} · ${attrs['UStateLastCycle'] ?? '-'} ms';
      case 'SelfControl':
        return '第 ${attrs['startframe'] ?? '?'}–${attrs['endframe'] ?? '?'} 帧'
            ' · 转向 ${attrs['turnspeed'] ?? '-'} / 移速 ${attrs['movespeed'] ?? '-'}';
      case 'HideBody':
        return '第 ${attrs['startframe'] ?? '?'}–${attrs['endframe'] ?? '?'} 帧 · 类型 ${attrs['type'] ?? '-'}';
      case 'ForceField':
        return '第 ${attrs['startframe'] ?? '?'}–${attrs['endframe'] ?? '?'} 帧 · 推开 ${attrs['pushspeed'] ?? '-'}';
      default:
        return attrs.entries.map((e) => '${e.key}=${e.value}').join(' ');
    }
  }

  Future<void> blockElementEditOne(String state, String tag) async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    final list = blockListFor(state, tag);
    final result = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _BlockElementDialog(
        tag: tag,
        label: blockElementTagLabel(tag),
        initial: list.isEmpty ? null : list.first,
        ustates: ustateOptions,
      ),
    );
    if (result == null || !_sameWeaponContext(generation, id, profile, hash)) return;
    blockSet(state, tag, [result]);
  }

  /// 给某个状态添加：先选状态，再选元素类型。
  Future<void> blockElementPickAndAdd() async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    final all = [for (final s in (data?['states'] as List? ?? [])) '$s'];
    if (all.isEmpty || blockElementGroups.isEmpty) return;
    final state = await showDialog<String>(
      context: context,
      builder: (_) => _SimplePickDialog(
        title: '给哪个状态添加',
        hint: '搜索状态号',
        options: [
          for (final s in all) {'value': s, 'label': chainStateLabel(s)},
        ],
      ),
    );
    if (state == null || !_sameWeaponContext(generation, id, profile, hash)) return;
    final options = <Map<String, String>>[];
    for (final g in blockElementGroups) {
      for (final e in (g['elements'] as List? ?? [])) {
        final placement = '${(e as Map)['placement'] ?? ''}';
        // <Condition><Ustate> 是动作分支的触发条件，不是自身状态。
        // 动作执行时给自己挂状态必须通过直属 <AddBuff> 新建；已有条件
        // 分支仍在状态卡里展示并允许编辑，但不能从“添加元素”入口伪造。
        if (placement == 'condition') continue;
        var hint = '';
        if (placement == 'hit') {
          hint = '（写进命中判定点，仅当块里已有）';
        }
        options.add({
          'value': '${e['tag']}',
          'label': '${e['label']}（${e['tag']}）$hint',
        });
      }
    }
    final tag = await showDialog<String>(
      context: context,
      builder: (_) =>
          _SimplePickDialog(title: '添加哪种元素', hint: '搜索名称', options: options),
    );
    if (tag == null || !_sameWeaponContext(generation, id, profile, hash)) return;
    await blockElementEditOne(state, tag);
  }

  /// 将本次防护编辑合并到当前武器内存，不写正式作者态或客户端归档。
  Future<void> saveBlockElements() async {
    if (blockElementsEdit.isEmpty) {
      setState(() => message = '没有改动，无需保存');
      return;
    }
    setState(() {
      final merged = _decodeBlockElements(blockElementsSaved);
      blockElementsEdit.forEach((state, tags) {
        final target = merged.putIfAbsent(state, () => {});
        tags.forEach((tag, list) {
          target[tag] = [
            for (final e in list)
              {
                'tag': tag,
                'attrs': e['attrs'],
                if ((e['box'] as List? ?? []).isNotEmpty) 'box': e['box'],
              },
          ];
        });
      });
      blockElementsSaved = merged;
      blockElementsEdit = {};
      blockElementEditing = false;
      dirty = true;
      failed = false;
      message = '防护/自身状态已写入当前武器内存；点击“保存方案”后才生成暂存副本。';
      workspace = _snapshotWorkspace();
    });
  }

  Future<void> clearBlockElements() async {
    if (!await confirmDestructive(
      '清除防护/自身状态定制？',
      '这会删掉该武器已保存的霸体/无敌/穿人/自身状态等编辑，动作块回到原样。此操作只修改当前内存工作区。',
    )) {
      return;
    }
    if (!mounted) return;
    setState(() {
      blockElementsSaved = {};
      blockElementsEdit = {};
      blockElementEditing = false;
      dirty = true;
      failed = false;
      message = '防护/自身状态已在当前武器内存中清除；点击“保存方案”后才生成暂存副本。';
      workspace = _snapshotWorkspace();
    });
  }

  /// 防护（霸体/无敌/穿人）与自身状态（UState/AddBuff）编辑卡。
  /// 这些都是动作块里的元素、按帧窗口生效；没编辑的状态沿用块里原有的。
  Widget blockElementCard() {
    final states = blockElementStates;
    final hasEdit = blockElementsSaved.isNotEmpty;
    return Padding(
      padding: const EdgeInsets.only(top: 6),
      child: Container(
        width: double.infinity,
        padding: const EdgeInsets.all(8),
        decoration: BoxDecoration(
          color: Colors.deepPurple.shade50,
          borderRadius: BorderRadius.circular(6),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(
                  Icons.security_outlined,
                  size: 16,
                  color: Colors.deepPurple.shade700,
                ),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    blockElementEditing
                        ? '防护与自身状态 · 编辑中'
                              '${blockElementsEdit.isEmpty ? '' : '（改过 ${blockElementsEdit.length} 个状态）'}'
                        : '防护与自身状态 · ${states.length} 个状态',
                    style: TextStyle(
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                      color: Colors.deepPurple.shade900,
                    ),
                  ),
                ),
                if (blockElementEditing) ...[
                  TextButton(
                    onPressed: busy ? null : cancelBlockElementEdit,
                    child: const Text('取消'),
                  ),
                  FilledButton.icon(
                    onPressed: busy ? null : saveBlockElements,
                    icon: const Icon(Icons.check, size: 16),
                    label: const Text('保存'),
                  ),
                ] else
                  TextButton.icon(
                    onPressed: busy ? null : startBlockElementEdit,
                    icon: const Icon(Icons.edit, size: 15),
                    label: Text(states.isEmpty ? '添加' : '编辑'),
                  ),
              ],
            ),
            if (states.isEmpty) ...[
              const SizedBox(height: 2),
              Text(
                blockElementEditing
                    ? '点下面「给某个状态添加」：霸体/无敌/穿人，或给这一招挂状态。'
                    : '动作块里没有额外防护，也没有状态元素。霸体/无敌/穿人是按帧生效的；'
                          '给自己挂状态用的是 AddBuff。命中给目标、触发条件只在块里本来就有对应结构时可改。',
                style: const TextStyle(fontSize: 12, color: Colors.black54),
              ),
            ],
            for (final state in states) blockElementStateGroup(state),
            if (blockElementEditing) ...[
              const SizedBox(height: 2),
              TextButton.icon(
                onPressed: busy ? null : blockElementPickAndAdd,
                icon: const Icon(Icons.add, size: 16),
                label: const Text('给某个状态添加', style: TextStyle(fontSize: 12)),
              ),
            ],
            if (!blockElementEditing && hasEdit)
              TextButton(
                onPressed: busy ? null : clearBlockElements,
                child: const Text(
                  '清除本武器的防护/自身状态定制（动作块回到原样）',
                  style: TextStyle(fontSize: 12),
                ),
              ),
          ],
        ),
      ),
    );
  }

  Widget blockElementStateGroup(String state) {
    final tags = blockTagsOf(state);
    return Padding(
      padding: const EdgeInsets.only(top: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            chainStateLabel(state),
            style: TextStyle(
              fontFamily: 'monospace',
              fontSize: 12,
              fontWeight: FontWeight.w600,
              color: Colors.deepPurple.shade900,
            ),
          ),
          for (final tag in tags)
            for (
              var index = 0;
              index < blockListFor(state, tag).length;
              index++
            )
              Padding(
                padding: const EdgeInsets.only(left: 10, top: 1),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    if (index == 0)
                      SizedBox(
                        width: 76,
                        child: Text(
                          blockElementTagLabel(tag),
                          style: const TextStyle(
                            fontSize: 11,
                            color: Colors.black54,
                          ),
                        ),
                      )
                    else
                      const SizedBox(width: 76),
                    Expanded(
                      child: Text(
                        blockElementLine(
                          tag,
                          (blockListFor(state, tag)[index]['attrs'] as List)
                              .map((a) => Map<String, dynamic>.from(a as Map))
                              .toList(),
                        ),
                        style: TextStyle(
                          fontFamily: 'monospace',
                          fontSize: 11,
                          color: blockTouched(state, tag)
                              ? Colors.deepPurple.shade800
                              : Colors.brown.shade800,
                        ),
                      ),
                    ),
                    if (blockElementEditing) ...[
                      GestureDetector(
                        onTap: busy
                            ? null
                            : () {
                                final list = [...blockListFor(state, tag)]
                                  ..removeAt(index);
                                blockSet(state, tag, list);
                              },
                        child: const Padding(
                          padding: EdgeInsets.only(left: 6),
                          child: Icon(
                            Icons.close,
                            size: 13,
                            color: Colors.deepOrange,
                          ),
                        ),
                      ),
                      GestureDetector(
                        onTap: busy
                            ? null
                            : () => blockElementEditOne(state, tag),
                        child: const Padding(
                          padding: EdgeInsets.only(left: 8, right: 2),
                          child: Icon(
                            Icons.edit,
                            size: 13,
                            color: Colors.blueGrey,
                          ),
                        ),
                      ),
                    ],
                  ],
                ),
              ),
        ],
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
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
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
    if (picked == null || !_sameWeaponContext(generation, id, profile, hash)) return;
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
                  comboEdgeChip(
                    edges[i],
                    editing
                        ? () => setState(
                            () => chainDraft.removeWhere(
                              (e) =>
                                  identical(e, edges[i]) ||
                                  (e['old'] == edges[i]['old'] &&
                                      e['new'] == edges[i]['new'] &&
                                      e['key'] == edges[i]['key']),
                            ),
                          )
                        : null,
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget comboEdgeChip(Map<String, String> edge, VoidCallback? onDelete) {
    final text =
        '${edge['key_label'] ?? keyName(edge['key'] ?? '')} → ${edge['new']}';
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: Colors.teal.shade50,
        borderRadius: BorderRadius.circular(6),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            text,
            style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
          ),
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
                  decoration: const InputDecoration(
                    labelText: '老状态',
                    isDense: true,
                  ),
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
                  decoration: const InputDecoration(
                    labelText: '按键',
                    isDense: true,
                  ),
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
                  decoration: const InputDecoration(
                    labelText: '新状态',
                    isDense: true,
                  ),
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

  void _restoreWorkspace(WeaponWorkspace restored) {
    restored = WeaponWorkspace.fromJson(restored.toJson());
    weapon = Map<String, dynamic>.from(restored.weapon);
    data = Map<String, dynamic>.from(restored.data);
    remaps = restored.remaps;
    cleared = restored.cleared;
    extraProperties = restored.extraProperties;
    hitProperties = {
      for (final entry in restored.hitProperties.entries)
        '${entry.key}': _hitValues(entry.value),
    };
    _structureBaseline = Map<String, dynamic>.from(
      restored.extra['structure_baseline'] as Map? ?? restored.weapon,
    );
    rules = [for (final row in restored.rules) Map<String, dynamic>.from(row)];
    comboChain = [
      for (final row in restored.comboChain) Map<String, String>.from(row),
    ];
    comboDeadEnds = [
      for (final row in restored.comboDeadEnds) Map<String, String>.from(row),
    ];
    frameSwitches = [
      for (final row in restored.frameSwitches) Map<String, dynamic>.from(row),
    ];
    frameEdits = restored.frameEdits;
    frameSaved = restored.frameSaved;
    counters = [
      for (final row in restored.counters) Map<String, dynamic>.from(row),
    ];
    counterEdits = restored.counterEdits;
    counterSaved = restored.counterSaved;
    blockElements = [
      for (final row in restored.blockElements) Map<String, dynamic>.from(row),
    ];
    blockElementsSaved = restored.blockElementsSaved;
    blockElementsEdit = restored.blockElementsEdit;
    variants = restored.variants;
    variantsSaved = restored.variantsSaved;
    variantsEdit = restored.variantsEdit;
    variantBases = restored.variantBases;
    variantOccupiedIDs = {...restored.variantOccupiedIDs};
    final serverIDs = restored.extra['variant_server_occupied_ids'];
    variantServerOccupiedIDs = {
      for (final id in (serverIDs is List ? serverIDs : const [])) '$id',
    };
    stageTracks = restored.stageTracks;
    scopeSaved = restored.scopeSaved;
    effectRows = restored.effectRows == null
        ? null
        : [
            for (final row in restored.effectRows!)
              Map<String, dynamic>.from(row),
          ];
    stageEffects = {
      for (final entry in restored.stageEffects.entries)
        entry.key: [
          for (final row in entry.value) Map<String, dynamic>.from(row),
        ],
    };
    comboRuleInfo = restored.comboRuleInfo;
    comboRuleMaxDraft = restored.comboRuleMaxDraft;
    comboRuleBlackDraft = restored.comboRuleBlackDraft;
    comboRuleWhiteDraft = restored.comboRuleWhiteDraft;
    variantSkillProMin =
        int.tryParse('${restored.extra['variant_skillpro_min'] ?? ''}') ??
        variantSkillProMin;
    variantSkillProMax =
        int.tryParse('${restored.extra['variant_skillpro_max'] ?? ''}') ??
        variantSkillProMax;
    comboRuleVersion++;
    ustateOptions = [
      for (final row in (restored.extra['ustate_options'] as List? ?? const []))
        Map<String, String>.from(row as Map),
    ];
    comboKeys = [
      for (final row in (restored.extra['combo_keys'] as List? ?? const []))
        Map<String, String>.from(row as Map),
    ];
    editorVersion++;
  }

  WeaponWorkspace _snapshotWorkspace() {
    final selected = weapon;
    if (selected == null) {
      throw StateError('Cannot snapshot an empty weapon workspace');
    }
    return WeaponWorkspace.fromPage(
      weapon: selected,
      data: data ?? const {},
      rules: rules,
      comboChain: comboChain,
      comboDeadEnds: comboDeadEnds,
      frameSwitches: frameSwitches,
      frameEdits: frameEdits,
      frameSaved: frameSaved,
      counters: counters,
      counterEdits: counterEdits,
      counterSaved: counterSaved,
      blockElements: blockElements,
      blockElementsSaved: blockElementsSaved,
      blockElementsEdit: blockElementsEdit,
      variants: variants,
      variantsSaved: variantsSaved,
      variantsEdit: variantsEdit,
      variantBases: variantBases,
      variantOccupiedIDs: variantOccupiedIDs,
      stageTracks: stageTracks,
      scopeSaved: scopeSaved,
      comboRuleInfo: comboRuleInfo,
      comboRuleMaxDraft: comboRuleMaxDraft,
      comboRuleBlackDraft: comboRuleBlackDraft,
      comboRuleWhiteDraft: comboRuleWhiteDraft,
      effectRows: effectRows,
      stageEffects: stageEffects,
      remaps: remaps,
      cleared: cleared,
      extraProperties: extraProperties,
      hitProperties: hitProperties,
      extra: {
        'structure_baseline': _structureBaseline,
        'variant_server_occupied_ids': variantServerOccupiedIDs.toList()
          ..sort(),
        'variant_skillpro_min': variantSkillProMin,
        'variant_skillpro_max': variantSkillProMax,
        'ustate_options': ustateOptions,
        'combo_keys': comboKeys,
      },
    );
  }

  void clearWeaponDetails() {
    editorVersion++;
    workspace = null;
    remaps = {};
    cleared = {};
    extraProperties = {};
    hitProperties = {};
    _structureBaseline = {};
    selectedAction = null;
    editUnlocked = false;
    comboChain = [];
    comboDeadEnds = [];
    comboKeys = [];
    chainEditing = false;
    chainDraft = [];
    chainOld = chainKey = chainNew = null;
    addStatePick = null;
    frameSwitches = [];
    frameSaved = {};
    frameEdits = {};
    frameKeys = [];
    frameEditing = false;
    counters = [];
    counterSaved = {};
    counterEdits = {};
    counterEditing = false;
    blockElements = [];
    blockElementsSaved = {};
    blockElementsEdit = {};
    blockElementGroups = [];
    blockElementEditing = false;
    variants = {};
    variantsSaved = {};
    variantsEdit = {};
    variantBases = {};
    variantEditing = false;
    variantServerOccupiedIDs = {};
    variantOccupiedIDs = {};
    _variantRulesSnapshot = null;
    _variantOccupiedSnapshot = null;
    _variantDirtySnapshot = false;
    stageTracks = {};
    scopeSaved = {};
    comboRuleInfo = {};
    comboRuleFailure = '';
    comboRuleEditing = false;
    syncComboRuleDraft();
    _effectThumbs = {};
    effectRows = null;
    stageEffects = {};
  }

  Future<void> select(Map<String, dynamic> value) async {
    final generation = ++_selectionGeneration;
    final profile = activeProfile;
    final hash = sourceHash;
    setState(() {
      clearWeaponDetails();
      weapon = null;
      rules = [];
      busy = true;
      failed = false;
    });
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({'operation': 'weapon_detail', 'weapon': value['id'], ..._profileContextFor(profile, hash)}),
      );
      if (!mounted || generation != _selectionGeneration ||
          activeProfile != profile || sourceHash != hash) return;
      _validateProfileResponse(result, profile, hash);
      final detail = Map<String, dynamic>.from(result['weapon'] as Map);
      setState(() {
        final weapons = [
          for (final row in (data?['weapons'] as List? ?? []))
            row['id'] == detail['id'] ? detail : row,
        ];
        data = {...?data, ...result, 'weapons': weapons};
        data!.remove('chain_info');
        data!.remove('combo_rule_info');
        data!.remove('remap_error');
        data!.remove('counter_warnings');
        if (result.containsKey('remap_error')) {
          data!['remap_error'] = result['remap_error'];
        }
        if (result.containsKey('counter_warnings')) {
          data!['counter_warnings'] = result['counter_warnings'];
        }
        weapon = detail;
        _structureBaseline = Map<String, dynamic>.from(
          jsonDecode(jsonEncode(detail)) as Map,
        );
        remaps = Map<String, dynamic>.from(
          jsonDecode(jsonEncode(result['remaps'] ?? const {})) as Map,
        );
        cleared = Map<String, dynamic>.from(
          jsonDecode(jsonEncode(result['cleared'] ?? const {})) as Map,
        );
        extraProperties = Map<String, dynamic>.from(
          jsonDecode(jsonEncode(result['extra_properties'] ?? const {})) as Map,
        );
        hitProperties = {
          for (final entry
              in ((result['hit_properties'] as Map?) ?? const {}).entries)
            '${entry.key}': _hitValues(entry.value),
        };
        final stored = data!['drafts']['${detail['id']}'] as List? ?? [];
        rules = (detail['stages'] as List).map((stage) {
          final saved = stored.where((r) => r['stage'] == stage['stage']);
          final rule = saved.isEmpty
              ? <String, dynamic>{
                  'stage': stage['stage'],
                  'buff': 0,
                  'level': 1,
                  'duration': 3000,
                }
              : Map<String, dynamic>.from(saved.first);
          for (final entry
              in ((rule['properties'] as Map?) ?? const {}).entries) {
            hitProperties.putIfAbsent(
              '${entry.key}',
              () => _hitValues(entry.value),
            );
          }
          return rule;
        }).toList();
        ustateOptions = [
          for (final u in (result['ustates'] as List? ?? []))
            {'id': '${(u as Map)['id']}', 'name': '${u['name'] ?? ''}'},
        ];
        dirty = false;
      });
      await refreshChain(
        detail['id'],
        snapshot: Map<String, dynamic>.from(result['chain_info'] as Map),
      );
      if (!_sameWeaponContext(generation, detail['id'], profile, hash)) return;
      await refreshComboRule(
        detail['id'],
        snapshot: Map<String, dynamic>.from(result['combo_rule_info'] as Map),
      );
      if (!_sameWeaponContext(generation, detail['id'], profile, hash)) return;
      setState(() {
        workspace = _snapshotWorkspace();
        workspaceExists = false;
        workspaceSavedAt = '';
      });
    } catch (error) {
      if (mounted && generation == _selectionGeneration &&
          activeProfile == profile && sourceHash == hash) {
        setState(() {
          failed = true;
          message = '武器 ${value['id']} 配置读取失败：$error';
        });
      }
    } finally {
      if (mounted && generation == _selectionGeneration &&
          activeProfile == profile && sourceHash == hash) {
        setState(() => busy = false);
      }
    }
  }

  Future<bool> discard() async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    if (!dirty) return true;
    final action = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('有未保存的修改'),
        content: const Text('先保存方案可以保留当前修改；保存方案不会直接修改游戏。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('继续编辑'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, 'discard'),
            child: const Text('丢弃修改'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, 'save'),
            child: const Text('保存后继续'),
          ),
        ],
      ),
    );
    if (!_sameWeaponContext(generation, id, profile, hash) || action == null) return false;
    if (action == 'save') {
      await execute('weapon_save');
      return _sameWeaponContext(generation, id, profile, hash) && !dirty;
    }
    return true;
  }

  /// 特效视图（含缩略图 data URI）缓存：编号 → 图。
  Map<String, String> _effectThumbs = {};

  /// null means inherit the backend/common registration; an empty list is explicit.
  List<Map<String, dynamic>>? effectRows;

  /// State -> authoritative stage effects; an empty list explicitly removes them.
  Map<String, List<Map<String, dynamic>>> stageEffects = {};

  Future<Map<String, dynamic>> _loadEffectView(int id) async {
    final generation = _selectionGeneration;
    final profile = activeProfile;
    final hash = sourceHash;
    final view = Map<String, dynamic>.from(
      await widget.api({'operation': 'weapon_effect_view', 'weapon': id, ..._profileContextFor(profile, hash)}),
    );
    if (!_sameWeaponContext(generation, id, profile, hash)) {
      throw StateError('当前武器已切换');
    }
    _validateProfileResponse(view, profile, hash);
    final baseRegistered = <String, Map<String, dynamic>>{
      for (final row in (view['registered'] as List? ?? []))
        '${(row as Map)['effect_id']}': Map<String, dynamic>.from(row),
    };
    final registered = effectRows == null
        ? baseRegistered.values.toList()
        : [
            for (final row in effectRows!)
              {
                ...?baseRegistered['${row['effect_id']}'],
                ...Map<String, dynamic>.from(row),
              },
          ];
    view['registered'] = registered;
    if (effectRows != null) {
      view['unregistered'] = <dynamic>[];
      view['common_count'] = registered.length;
    }
    final references = [
      for (final ref in (view['references'] as List? ?? []))
        Map<String, dynamic>.from(ref as Map),
    ];
    for (final entry in stageEffects.entries) {
      references.removeWhere((row) => '${row['state']}' == entry.key);
      references.addAll([
        for (final row in entry.value) {'state': entry.key, ...row},
      ]);
    }
    view['references'] = references;
    final thumbs = <String, String>{};
    for (final entry in (view['registered'] as List? ?? [])) {
      final map = Map<String, dynamic>.from(entry as Map);
      final data = '${map['thumbnail'] ?? ''}';
      if (data.isNotEmpty) thumbs['${map['effect_id']}'] = data;
    }
    _effectThumbs = thumbs;
    return view;
  }

  /// 武器级特效：预览登记了什么、招式引用了什么，并允许增删登记（自建武器）。
  Future<void> openEffectEditor() async {
    final selected = weapon;
    if (selected == null || busy) return;
    final generation = _selectionGeneration;
    final profile = activeProfile;
    final hash = sourceHash;
    setState(() => busy = true);
    try {
      final view = await _loadEffectView(selected['id'] as int);
      if (!_sameWeaponContext(generation, selected['id'], profile, hash)) return;
      setState(() => busy = false);
      final saved = await showDialog<bool>(
        context: context,
        builder: (_) => _EffectLedgerDialog(
          title: '${selected['name']}',
          editable: canEdit(selected['id']),
          view: view,
          onSubmit: (rows) async {
            if (!_sameWeaponContext(generation, selected['id'], profile, hash) ||
                !canEdit(selected['id'])) return;
            setState(() {
              effectRows = [
                for (final row in rows) Map<String, dynamic>.from(row),
              ];
              dirty = true;
              workspace = _snapshotWorkspace();
              message = '特效登记已写入当前武器内存；点击“保存方案”提交暂存。';
            });
          },
        ),
      );
      if (!_sameWeaponContext(generation, selected['id'], profile, hash)) return;
      if (saved == true) {
        setState(() => message = '特效登记已写入当前武器内存；点击“保存方案”提交暂存。');
      }
    } catch (e) {
      if (_sameWeaponContext(generation, selected['id'], profile, hash)) {
        setState(() {
          busy = false;
          message = '$e';
        });
      }
    }
  }

  /// 招式级特效：预览并编辑该状态动作块里的 <Effect> / <HitEffect>。
  /// 共用动作块由后端克隆成该武器独占，原有招式不受影响。
  Future<void> openStageEffects(int index) async {
    final selected = weapon;
    if (selected == null || busy) return;
    final generation = _selectionGeneration;
    final profile = activeProfile;
    final hash = sourceHash;
    final stage = Map<String, dynamic>.from(selected['stages'][index] as Map);
    setState(() => busy = true);
    try {
      final view = await _loadEffectView(selected['id'] as int);
      if (!_sameWeaponContext(generation, selected['id'], profile, hash)) return;
      setState(() => busy = false);
      final state = '${stage['state']}';
      Map<String, dynamic> stageEffectRow(Map row) => {
        'kind': '${row['kind'] ?? 'effect'}',
        'effect_id': '${row['effect_id']}',
        'start': row['start'] ?? 0,
        'end': row['end'] ?? 0,
        'bind_type': '${row['bind_type'] ?? ''}',
        'bind_index': '${row['bind_index'] ?? ''}',
        'break': '${row['break'] ?? ''}',
      };
      final current = stageEffects.containsKey(state)
          ? [for (final row in stageEffects[state]!) stageEffectRow(row)]
          : [
              for (final ref in (view['references'] as List? ?? []))
                if ('${(ref as Map)['state']}' == state) stageEffectRow(ref),
            ];
      final saved = await showDialog<bool>(
        context: context,
        builder: (_) => _StageEffectDialog(
          title: '状态 $state · ${stage['label'] ?? ''}',
          editable: canEdit(selected['id']),
          rows: current,
          thumbs: _effectThumbs,
          onSubmit: (rows) async {
            if (!_sameWeaponContext(generation, selected['id'], profile, hash) ||
                !canEdit(selected['id'])) return;
            setState(() {
              stageEffects[state] = [
                for (final row in rows) Map<String, dynamic>.from(row),
              ];
              dirty = true;
              workspace = _snapshotWorkspace();
              message = '招式特效已写入当前武器内存；点击“保存方案”提交暂存。';
            });
          },
        ),
      );
      if (!_sameWeaponContext(generation, selected['id'], profile, hash)) return;
      if (saved == true) {
        setState(() => message = '招式特效已写入当前武器内存；点击“保存方案”提交暂存。');
      }
    } catch (e) {
      if (_sameWeaponContext(generation, selected['id'], profile, hash)) {
        setState(() {
          busy = false;
          message = '$e';
        });
      }
    }
  }

  Future<void> repairEffects() async {
    final selected = weapon;
    if (selected == null || busy) return;
    final generation = _selectionGeneration;
    final profile = activeProfile;
    final hash = sourceHash;
    setState(() {
      busy = true;
      message = '';
    });
    try {
      final preview = Map<String, dynamic>.from(
        await widget.api({
          'operation': 'weapon_effects_preview',
          'weapon': selected['id'],
          ..._profileContextFor(profile, hash),
        }),
      );
      if (!_sameWeaponContext(generation, selected['id'], profile, hash)) return;
      _validateProfileResponse(preview, profile, hash);
      final additions = preview['additions'] as List;
      final issues = preview['issues'] as List;
      final confirm = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text('${selected['name']} · 攻击特效'),
          content: SizedBox(
            width: 560,
            child: SingleChildScrollView(
              child: Text(
                [
                  '读取：${preview['path']}',
                  additions.isEmpty
                      ? '没有可自动补齐的特效。'
                      : '可补齐 ${additions.length} 条：',
                  ...additions.map((e) => '${e['id']} → ${e['file']}'),
                  if (issues.isNotEmpty) '\n以下项目需要手动处理：',
                  ...issues.map((e) => '$e'),
                  '\n仅补充特效加载登记，不修改招式、伤害和 BUFF。写入前自动备份，重启游戏后生效；不会自动发布到线上。',
                ].join('\n'),
              ),
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('关闭'),
            ),
            if (additions.isNotEmpty)
              FilledButton(
                onPressed: () => Navigator.pop(context, true),
                child: const Text('备份并补齐'),
              ),
          ],
        ),
      );
      if (confirm != true || !_sameWeaponContext(generation, selected['id'], profile, hash)) return;
      final view = await _loadEffectView(selected['id'] as int);
      if (!_sameWeaponContext(generation, selected['id'], profile, hash)) return;
      final merged = <String, Map<String, dynamic>>{
        for (final row in (view['registered'] as List? ?? []))
          '${(row as Map)['effect_id']}': {
            'effect_id': '${row['effect_id']}',
            'file': '${row['file']}',
          },
      };
      for (final row in additions) {
        final map = Map<String, dynamic>.from(row as Map);
        final id = '${map['id']}';
        if (id.isNotEmpty) {
          merged[id] = {'effect_id': id, 'file': '${map['file'] ?? id}'};
        }
      }
      setState(() {
        effectRows = merged.values.toList();
        dirty = true;
        workspace = _snapshotWorkspace();
        message = '已将 ${additions.length} 条攻击特效登记合并到当前内存；点击“保存方案”提交暂存。';
      });
    } catch (e) {
      if (_sameWeaponContext(generation, selected['id'], profile, hash)) {
        setState(() => message = '$e');
      }
    } finally {
      if (_sameWeaponContext(generation, selected['id'], profile, hash)) {
        setState(() => busy = false);
      }
    }
  }

  Future<void> execute(String operation) async {
    final selectedWeaponID = weapon?['id'];
    final generation = _selectionGeneration;
    final profile = activeProfile;
    final hash = sourceHash;
    if (selectedWeaponID == null) return;
    if (operation == 'weapon_save') {
      await saveWorkspace();
      return;
    }
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
      if (confirmed != true || !_sameWeaponContext(generation, selectedWeaponID, profile, hash)) return;
    }
    if (!_sameWeaponContext(generation, selectedWeaponID, profile, hash)) return;
    if (weapon != null) {
      workspace = _snapshotWorkspace();
    }
    setState(() {
      busy = true;
      failed = false;
      message = '';
    });
    try {
      final request = <String, dynamic>{
        'operation': operation,
        'weapon': weapon!['id'],
        'rules': rules,
        ..._profileContextFor(profile, hash),
        'workspace': workspace?.toJson(),
        'revision': data!['revision'],
      };
      final result = await widget.api(request);
      if (!mounted ||
          generation != _selectionGeneration ||
          weapon?['id'] != selectedWeaponID ||
          activeProfile != profile ||
          sourceHash != hash) return;
      _validateProfileResponse(result, profile, hash);
      setState(() {
        dirty = false;
        message = result['message'];
      });
      final reloadGeneration = _selectionGeneration + 1;
      await reloadSelectedWeapon();
      if (operation != 'weapon_save' &&
          _sameWeaponContext(reloadGeneration, selectedWeaponID, profile, hash)) {
        await reloadClient();
      }
    } catch (e) {
      if (mounted &&
          generation == _selectionGeneration &&
          weapon?['id'] == selectedWeaponID &&
          activeProfile == profile &&
          sourceHash == hash) {
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
  /// 同时包含配置与自制武器的模型、贴图、动作资源。
  Future<void> exportPackage() async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    if (!(form.currentState?.validate() ?? false)) return;
    final options = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _ExportDialog(
        weaponName: '${weapon!['name']}',
        createdCount: created.length,
      ),
    );
    if (options == null || !_sameWeaponContext(generation, id, profile, hash)) return;
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
          'weapon': id,
          ..._profileContextFor(profile, hash),
          'all': options['all'] == true,
          'include': include,
          'applied_only': options['applied_only'] == true,
          'rules': rules,
          'revision': data!['revision'],
        }) as Map,
      );
      if (!_sameWeaponContext(generation, id, profile, hash)) return;
      _validateProfileResponse(result, profile, hash);
      setState(() {
        exportResult = result;
        busy = false;
        message =
            '已导出 ${result['name']}（${sizeText(result['size'])}，'
            '${(result['files'] as List? ?? []).length} 个素材文件）。'
            '把包解压后覆盖到客户端根目录即可。';
      });
    } catch (e) {
      if (_sameWeaponContext(generation, id, profile, hash)) {
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
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    final all = await showDialog<bool>(
      context: context,
      builder: (_) => _MergeExportDialog(
        weaponName: '${weapon!['name']}',
        createdCount: created.length,
      ),
    );
    if (all == null || !_sameWeaponContext(generation, id, profile, hash)) return;
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
          'weapon': id,
          ..._profileContextFor(profile, hash),
          'all': all,
          'rules': rules,
          'revision': data!['revision'],
        }) as Map,
      );
      if (!_sameWeaponContext(generation, id, profile, hash)) return;
      _validateProfileResponse(result, profile, hash);
      setState(() {
        exportResult = result;
        busy = false;
        message =
            '已导出合并包 ${result['name']}（${sizeText(result['size'])}，'
            '${(result['files'] as List? ?? []).length} 个素材文件）。'
            '把它发到目标机器，在 GM 里点「导入武器包」选择该 zip 即可合并，'
            '目标客户端的其他配置不会被改动。';
      });
    } catch (e) {
      if (_sameWeaponContext(generation, id, profile, hash)) {
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
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    if (!await discard() || !_sameWeaponContext(generation, id, profile, hash)) return;
    final applied = await Navigator.push<bool>(
      context,
      MaterialPageRoute(builder: (_) => WeaponMergePage(api: widget.api)),
    );
    if (applied == true && _sameWeaponContext(generation, id, profile, hash)) await load();
  }

  /// 发版包导出结果卡片：路径、分组统计、配置改动、文件清单。
  Widget exportCard() {
    final files = [
      for (final f in (exportResult['files'] as List? ?? []))
        Map<String, dynamic>.from(f as Map),
    ];
    final missing = [
      for (final m in (exportResult['missing'] as List? ?? [])) '$m',
    ];
    final config = [
      for (final c in (exportResult['config_changes'] as List? ?? [])) '$c',
    ];
    final plans = [
      for (final p in (exportResult['plans'] as List? ?? []))
        Map<String, dynamic>.from(p as Map),
    ];
    final counts = Map<String, dynamic>.from(
      exportResult['counts'] as Map? ?? {},
    );
    final byKind = <String, List<Map<String, dynamic>>>{};
    for (final file in files) {
      byKind.putIfAbsent('${file['kind']}', () => []).add(file);
    }
    final kinds = [
      for (final k in packageSections.keys)
        if (byKind.containsKey(k)) k,
    ];
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
                  style: const TextStyle(
                    fontSize: 11,
                    color: Colors.deepOrange,
                  ),
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
                      style: const TextStyle(
                        fontSize: 11,
                        color: Colors.black54,
                      ),
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
    final generation = _selectionGeneration;
    final selectedWeaponID = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    if (!(await discard()) ||
        !_sameWeaponContext(generation, selectedWeaponID, profile, hash)) return;
    final blueprint = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _BlueprintDialog(
        types: [for (final t in (data?['types'] as List? ?? [])) t],
        models: [for (final m in (data?['models'] as List? ?? [])) '$m'],
        donors: donors,
        minID: data?['blueprint_min'] as int? ?? 253000,
        maxID: data?['blueprint_max'] as int? ?? 253999,
        suggestedID: suggestID(),
        usedIDs: {for (final id in usedIDs) '$id'},
        onUploadIcon: (sourcePath) async {
          final r = await _apiForWeaponContext(generation, selectedWeaponID, profile, hash)({
            'operation': 'weapon_icon_upload',
            'source_path': sourcePath,
          });
          return '${r['icon'] ?? ''}';
        },
      ),
    );
    if (blueprint == null || !_sameWeaponContext(generation, selectedWeaponID, profile, hash)) return;
    setState(() {
      busy = true;
      failed = false;
      message = '正在登记自建武器…';
    });
    try {
      final result = await widget.api({
        'operation': 'weapon_create',
        'blueprint': blueprint,
        ..._profileContextFor(profile, hash),
      });
      if (!_sameWeaponContext(generation, selectedWeaponID, profile, hash)) return;
      _validateProfileResponse(result, profile, hash);
      setState(() {
        dirty = false;
        message = '${result['message']}';
      });
      await load(prefer: blueprint['id'] as int);
    } catch (e) {
      if (_sameWeaponContext(generation, selectedWeaponID, profile, hash)) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  Future<void> forget() async {
    final generation = _selectionGeneration;
    final selectedWeaponID = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    if (!(await discard()) ||
        !_sameWeaponContext(generation, selectedWeaponID, profile, hash)) return;
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
    if (confirmed != true || !_sameWeaponContext(generation, selectedWeaponID, profile, hash)) return;
    setState(() {
      busy = true;
      failed = false;
      message = '正在移除自建武器…';
    });
    try {
      final result = await widget.api({
        'operation': 'weapon_forget',
        'weapon': id,
        ..._profileContextFor(profile, hash),
      });
      if (!_sameWeaponContext(generation, selectedWeaponID, profile, hash)) return;
      _validateProfileResponse(result, profile, hash);
      setState(() {
        dirty = false;
        message = '${result['message']}';
      });
      await load(prefer: 253013);
    } catch (e) {
      if (_sameWeaponContext(generation, selectedWeaponID, profile, hash)) {
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
    final generation = _selectionGeneration;
    final selectedWeaponID = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    if (!(await discard()) ||
        !_sameWeaponContext(generation, selectedWeaponID, profile, hash)) return;
    final id = weapon!['id'];
    final updated = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _BlueprintInfoDialog(
        initial: blueprintOf(id),
        onUploadIcon: (sourcePath) async {
          final r = await _apiForWeaponContext(generation, selectedWeaponID, profile, hash)({
            'operation': 'weapon_icon_upload',
            'source_path': sourcePath,
          });
          return '${r['icon'] ?? ''}';
        },
      ),
    );
    if (updated == null || !_sameWeaponContext(generation, selectedWeaponID, profile, hash)) return;
    setState(() {
      busy = true;
      failed = false;
      message = '正在更新武器信息…';
    });
    try {
      final result = await widget.api({
        'operation': 'weapon_blueprint_update',
        'weapon': id,
        ..._profileContextFor(profile, hash),
        'blueprint': updated,
      });
      if (!_sameWeaponContext(generation, selectedWeaponID, profile, hash)) return;
      _validateProfileResponse(result, profile, hash);
      setState(() {
        dirty = false;
        message = '${result['message']}';
      });
      await load(prefer: id);
    } catch (e) {
      if (_sameWeaponContext(generation, selectedWeaponID, profile, hash)) {
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
    final generation = _selectionGeneration;
    final selectedWeaponID = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    if (!(await discard()) ||
        !_sameWeaponContext(generation, selectedWeaponID, profile, hash)) return;
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
        ..._profileContextFor(profile, hash),
        'donor': donor,
      });
      if (!_sameWeaponContext(generation, selectedWeaponID, profile, hash)) return;
      _validateProfileResponse(result, profile, hash);
      setState(() {
        dirty = false;
        message = '${result['message']}';
      });
      await load(prefer: id);
    } catch (e) {
      if (_sameWeaponContext(generation, selectedWeaponID, profile, hash)) {
        setState(() {
          busy = false;
          failed = true;
          message = '$e';
        });
      }
    }
  }

  Future<void> pickComboDonor() async {
    final generation = _selectionGeneration;
    final selectedWeaponID = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    if (!(await discard()) ||
        !_sameWeaponContext(generation, selectedWeaponID, profile, hash)) return;
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
    if (donor == null || !_sameWeaponContext(generation, selectedWeaponID, profile, hash)) return;
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
    final original = _hitValues(hit);
    final current = _mergeHitValues([
      hit,
      (rule['properties'] as Map?)?['${hit['id']}'],
      hitProperties['${hit['id']}'],
    ]);
    final fields = (data?['fields'] as List? ?? []).where(
      (f) => f['key'] != 'SkillDamage' && f['key'] != 'SkillEnhanceDamage',
    );
    if (fields.every(
      (f) => '${current[f['key']]}' == '${original[f['key']]}',
    )) {
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
    final risky =
        desc.contains('操作键') ||
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
    final risky =
        label.contains('勿用') ||
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
  List<Map<String, dynamic>> extraHitOptions(dynamic field, int current) {
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
                final values = _hitValues(hitProperties['${hit['id']}']);
                values['${field['key']}'] = value;
                hitProperties['${hit['id']}'] = values;
                _projectHitPropertiesToRules();
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
                    '${_hitField('${hit['id']}', '${field['key']}', hit['values'][field['key']])}',
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
                    '${_hitField('${hit['id']}', '${field['key']}', hit['values'][field['key']])}',
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
                  final values = _hitValues(hitProperties['${hit['id']}']);
                  values[field['key']] = num.tryParse(text) ?? -1;
                  hitProperties['${hit['id']}'] = values;
                  _projectHitPropertiesToRules();
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
    final stageState = '${stage['state']}';
    final ctx = variantHitContext(stageState);
    final hits = [
      ...(stage['hits'] as List? ?? []),
      for (final branch in variantPendingFor(stageState))
        for (final segment in (branch['segments'] as List? ?? const []))
          if ('${(segment as Map)['skillproid'] ?? ''}'.isNotEmpty &&
              !(stage['hits'] as List? ?? const []).any(
                (hit) => '${(hit as Map)['id']}' == '${segment['skillproid']}',
              ))
            {
              'id': '${segment['skillproid']}',
              'variant': '${branch['condition']}',
              'buff': '0',
              'values': Map<String, dynamic>.from(
                (ctx.hits['${segment['template_skillproid'] ?? segment['skillproid']}']?['values']
                        as Map?) ??
                    const <String, dynamic>{},
              ),
            },
    ];
    return [
      for (final hit in hits)
        Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                if (hits.length > 1) Text('命中 ${hits.indexOf(hit) + 1}'),
                const SizedBox(width: 8),
                Text(
                  '命中属性 ${hit['id']}',
                  style: const TextStyle(fontSize: 11, fontFamily: 'monospace'),
                ),
                if ('${hit['variant'] ?? ''}'.isNotEmpty) ...[
                  if (hits.length > 1) const SizedBox(width: 8),
                  // 这条命中属性只在该分支下生效（动作块里的 <Condition><Ustate id>）。
                  // 同一个动作分了几套形态时，靠它分辨哪条是哪套。
                  Container(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 6,
                      vertical: 1,
                    ),
                    decoration: BoxDecoration(
                      color: Colors.deepPurple.shade50,
                      borderRadius: BorderRadius.circular(4),
                    ),
                    child: Text(
                      '分支 ${ustateLabel('${hit['variant']}')}',
                      style: TextStyle(
                        fontSize: 11,
                        color: Colors.deepPurple.shade700,
                        fontFamily: 'monospace',
                      ),
                    ),
                  ),
                ],
              ],
            ),
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
                        final id = '${hit['id']}';
                        final values = _mergeHitValues([
                          hit,
                          (rule['properties'] as Map?)?[id],
                          hitProperties[id],
                        ]);
                        if (value == 'original') {
                          final damage = {
                            for (final key in ['SkillDamage', 'SkillEnhanceDamage'])
                              if (values.containsKey(key)) key: values[key],
                          };
                          values
                            ..clear()
                            ..addAll(_hitValues(hit))
                            ..addAll(damage);
                        } else {
                          final effect = (data!['effects'] as List).firstWhere(
                            (e) => e['id'] == value,
                          );
                          values.addAll(_hitValues(effect['values']));
                        }
                        hitProperties[id] = values;
                        _projectHitPropertiesToRules();
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

  Map<String, dynamic> _stateMap(Map<String, dynamic> source) =>
      Map<String, dynamic>.from(source['${weapon!['id']}'] as Map? ?? const {});

  String _allocateTemplateProperty(String template, Object? hit) {
    final source = template.trim();
    if (source.isEmpty) return '';
    final owner = '${weapon!['id']}';
    for (final entry in extraProperties.entries) {
      final row = entry.value;
      if (row is Map &&
          '${row['template']}' == source &&
          '${row['owner_weapon'] ?? owner}' == owner) {
        return '${entry.key}';
      }
    }
    final used = <String>{
      ...hitProperties.keys,
      ...extraProperties.keys,
      ...variantOccupiedIDs,
      for (final stage in (weapon!['stages'] as List? ?? const []))
        for (final id in ((stage as Map)['property_ids'] as List? ?? const []))
          '$id',
    };
    final id = nextVariantSkillProID(
      used,
      minimum: 800000001,
      maximum: 899999999,
    );
    if (id == null) throw StateError('命中属性编号空间不足');
    extraProperties[id] = {'template': source, 'owner_weapon': owner};
    hitProperties[id] = _hitValues(hit);
    return id;
  }

  List<Map<String, dynamic>> _copyRows(Object? value) => [
    for (final row in (value as List? ?? const []))
      Map<String, dynamic>.from(jsonDecode(jsonEncode(row)) as Map),
  ];

  List<Map<String, dynamic>> _copyAttrsRows(Object? value) => [
    for (final row in (value as List? ?? const []))
      {
        'attrs': [
          for (final attr in ((row as Map)['attrs'] as List? ?? const []))
            {'key': '${(attr as Map)['key']}', 'value': '${attr['value']}'},
        ],
      },
  ];

  void _migrateTemplateModules(String targetState, Map<String, dynamic> value) {
    final sourceState = '${value['template_state'] ?? ''}';
    final frame = value['template_frame_switches'];
    if (frame is List && frame.isNotEmpty) {
      frameSaved[targetState] = _copyAttrsRows(frame);
      frameSwitches = [
        for (final entry in frameSaved.entries)
          for (final item in entry.value) {'state': entry.key, ...item},
      ];
    }
    final templateCounters = value['template_counters'];
    if (templateCounters is List && templateCounters.isNotEmpty) {
      final source = Map<String, dynamic>.from(templateCounters.first as Map);
      counterSaved[targetState] = {
        'attrs': [
          for (final attr in (source['attrs'] as List? ?? const []))
            {'key': '${(attr as Map)['key']}', 'value': '${attr['value']}'},
        ],
        'box': [
          for (final attr in (source['box'] as List? ?? const []))
            {'key': '${(attr as Map)['key']}', 'value': '${attr['value']}'},
        ],
      };
      counters = [
        for (final entry in counterSaved.entries)
          if (entry.value != null)
            {'state': entry.key, ...encodeCounter(entry.value!)},
      ];
    }
    final blocks = value['template_block_elements'];
    if (blocks is List && blocks.isNotEmpty) {
      final source = Map<String, dynamic>.from(blocks.first as Map);
      final elements = <String, List<Map<String, dynamic>>>{};
      for (final raw in (source['elements'] as List? ?? const [])) {
        final row = Map<String, dynamic>.from(raw as Map);
        final tag = '${row['tag']}';
        elements.putIfAbsent(tag, () => []).add({
          'tag': tag,
          'attrs': [
            for (final attr in (row['attrs'] as List? ?? const []))
              {'key': '${(attr as Map)['key']}', 'value': '${attr['value']}'},
          ],
          'box': [
            for (final attr in (row['box'] as List? ?? const []))
              {'key': '${(attr as Map)['key']}', 'value': '${attr['value']}'},
          ],
        });
      }
      if (elements.isNotEmpty) blockElementsSaved[targetState] = elements;
      blockElements = [
        for (final entry in blockElementsSaved.entries)
          for (final tag in entry.value.entries)
            for (final row in tag.value) {'state': entry.key, ...row},
      ];
    }
    final scope = value['template_scope_saved'];
    if (scope is Map && scope.isNotEmpty) {
      scopeSaved[targetState] = {
        for (final entry in scope.entries)
          '${entry.key}': [
            for (final attr in (entry.value as List? ?? const []))
              {'key': '${(attr as Map)['key']}', 'value': '${attr['value']}'},
          ],
      };
    }
    final effects = value['template_stage_effects'];
    if (effects is List && effects.isNotEmpty) {
      stageEffects[targetState] = _copyRows(effects);
    }
    final track = value['template_track'];
    if (track is Map) {
      final copied = Map<String, dynamic>.from(
        jsonDecode(jsonEncode(track)) as Map,
      );
      copied['state'] = targetState;
      copied['action'] = '${value['action'] ?? copied['action'] ?? ''}';
      stageTracks[targetState] = copied;
    }
    final templateVariants = value['template_variants'];
    final templateVariantsSaved = value['template_variants_saved'];
    if (sourceState.isNotEmpty) {
      final sourceVariants = templateVariants is Map
          ? templateVariants[sourceState]
          : null;
      final sourceSaved = templateVariantsSaved is Map
          ? templateVariantsSaved[sourceState]
          : null;
      final rows = sourceVariants is List
          ? _copyRows(sourceVariants)
          : sourceSaved is List
              ? _copyRows(sourceSaved)
              : const <Map<String, dynamic>>[];
      if (rows.isNotEmpty) {
        variants[targetState] = rows;
        variantsSaved[targetState] = [
          for (final row in rows) Map<String, dynamic>.from(row),
        ];
      }
    }
    final templateBases = value['template_variant_bases'];
    if (templateBases is Map && sourceState.isNotEmpty) {
      final sourceBase = templateBases[sourceState];
      if (sourceBase is List) {
        variantBases[targetState] = _copyRows(sourceBase);
      }
    }
    final chain = value['template_chain'];
    if (chain is List && sourceState.isNotEmpty && sourceState != targetState) {
      final migrated = [
        for (final raw in chain)
          if ('${(raw as Map)['old']}' == sourceState ||
              '${raw['new']}' == sourceState)
            {
              'old': '${raw['old']}' == sourceState
                  ? targetState
                  : '${raw['old']}',
              'new': '${raw['new']}' == sourceState
                  ? targetState
                  : '${raw['new']}',
              'key': '${raw['key']}',
            },
      ];
      final merged = [for (final row in comboChain) Map<String, String>.from(row)];
      for (final row in migrated) {
        if (!merged.any(
          (existing) => existing['old'] == row['old'] &&
              existing['new'] == row['new'] &&
              existing['key'] == row['key'],
        )) {
          merged.add(Map<String, String>.from(row));
        }
      }
      comboChain = merged;
    }
  }

  void _clearStateEdits(String stateKey) {
    for (final entries in [
      frameSaved,
      frameEdits,
      counterSaved,
      counterEdits,
      blockElementsSaved,
      blockElementsEdit,
      variants,
      variantsSaved,
      variantsEdit,
      variantBases,
      stageTracks,
      scopeSaved,
      stageEffects,
    ]) {
      entries.remove(stateKey);
    }
    for (final rows in [frameSwitches, counters, blockElements]) {
      rows.removeWhere((row) => '${row['state']}' == stateKey);
    }
  }

  void commitRemap(String stateKey, Map<String, dynamic> value) {
    setState(() {
      final key = '${weapon!['id']}';
      final per = _stateMap(remaps);
      final action = '${value['action'] ?? ''}'.trim();
      var property = '${value['property_id'] ?? ''}'.trim();
      if (value['allocate_property'] == true && property.isNotEmpty) {
        final template = '${value['property_id']}';
        property = _allocateTemplateProperty(template, value['hit']);
        value = {
          ...value,
          'template_property_id': template,
          'property_id': property,
        };
      }
      if (action.isEmpty && property.isEmpty) {
        _clearStateEdits(stateKey);
        per.remove(stateKey);
      } else {
        per[stateKey] = {...value, 'action': action, 'property_id': property};
        final deleted = _stateMap(cleared)..remove(stateKey);
        cleared[key] = deleted;
      }
      remaps[key] = per;
      _migrateTemplateModules(stateKey, value);
      final stages = List<dynamic>.from(weapon!['stages'] as List);
      final index = stages.indexWhere((s) => '${s['state']}' == stateKey);
      final baseline = (_structureBaseline['stages'] as List? ?? const [])
          .where((s) => '${s['state']}' == stateKey);
      final previous = index >= 0
          ? Map<String, dynamic>.from(stages[index] as Map)
          : <String, dynamic>{};
      final templateStage = value['template_stage_data'] is Map
          ? Map<String, dynamic>.from(value['template_stage_data'] as Map)
          : <String, dynamic>{};

      if (action.isEmpty && property.isEmpty) {
        if (baseline.isNotEmpty) {
          final original = jsonDecode(jsonEncode(baseline.first));
          if (index >= 0) {
            stages[index] = original;
          } else {
            stages.add(original);
          }
        } else if (index >= 0) {
          stages.removeAt(index);
        }
      } else {
        final number = int.parse(stateKey);
        final suppliedHit = value['hit'] is Map
            ? Map<String, dynamic>.from(value['hit'] as Map)
            : null;
        final templateProperty = '${value['template_property_id'] ?? ''}';
        final templateHit = templateProperty.isNotEmpty
            ? hitProperties[templateProperty]
            : null;
        final hit = property.isEmpty
            ? null
            : {
                ...?suppliedHit,
                'id': property,
                'buff': suppliedHit?['buff'] ??
                    (templateHit is Map ? templateHit['buff'] : '0') ??
                    '0',
                'values': _mergeHitValues([templateHit, suppliedHit]),
              };
        if (property.isNotEmpty && hit != null) {
          hitProperties[property] = _hitValues(hit);
        }
        final row = <String, dynamic>{
          if (baseline.isNotEmpty)
            ...Map<String, dynamic>.from(baseline.first as Map),
          ...templateStage,
          'action': '',
          'stage': number >= 2011 && number <= 2016 ? number - 2010 : number,
          'state': stateKey,
          'label': value['label'] == null || '${value['label']}'.isEmpty
              ? (index >= 0 ? stages[index]['label'] : stateKey)
              : value['label'],
          'supported': true, 'reason': '',
          if (action.isNotEmpty) 'action': action,
          'property_ids': <dynamic>[
            ...(templateStage['property_ids'] as List? ??
                previous['property_ids'] as List? ??
                const []),
            if (property.isNotEmpty &&
                !(templateStage['property_ids'] as List? ??
                        previous['property_ids'] as List? ??
                        const [])
                    .map((id) => '$id')
                    .contains(property))
              property,
          ],
          'hits': [
            for (final oldHit in (templateStage['hits'] as List? ??
                previous['hits'] as List? ??
                const []))
              Map<String, dynamic>.from(oldHit as Map),
            if (hit != null &&
                !(templateStage['hits'] as List? ??
                        previous['hits'] as List? ??
                        const [])
                    .any((oldHit) => '${(oldHit as Map)['id']}' == property))
              hit,
          ],
        };
        if (index >= 0) {
          stages[index] = row;
        } else {
          stages.add(row);
        }
      }
      weapon = {...weapon!, 'stages': stages};
      final oldRules = {for (final r in rules) '${r['stage']}': r};
      final changed = int.parse(stateKey);
      final ruleStage = changed >= 2011 && changed <= 2016
          ? changed - 2010
          : changed;
      rules = [
        for (final s in stages)
          {
            'buff': 0,
            'level': 1,
            'duration': 3000,
            ...?oldRules['${s['stage']}'],
            'stage': s['stage'],
            if (s['stage'] == ruleStage && action.isNotEmpty) ...{
              'buff': 0,
              'level': 1,
              'duration': 3000,
            },
          },
      ];
      _projectHitPropertiesToRules();
      editorVersion++;
      _markWorkspaceDirty('重映射已更新到内存工作区；点击“保存方案”提交暂存。');
    });
  }

  String allocateProperty(String template, List<Map<String, dynamic>> catalog) {
    final used = {
      for (final p in catalog) '${p['id']}',
      ...extraProperties.keys,
      ...variantOccupiedIDs,
    };
    final id = nextVariantSkillProID(
      used,
      minimum: 800000001,
      maximum: 899999999,
    );
    if (id == null) throw StateError('命中属性编号空间不足');
    setState(() {
      extraProperties[id] = {
        'template': template,
        'owner_weapon': '${weapon!['id']}',
      };
      final templateHit = hitProperties[template];
      final catalogHit = catalog
          .where((row) => '${row['id']}' == template)
          .firstOrNull;
      hitProperties[id] = templateHit is Map
          ? _hitValues(templateHit)
          : catalogHit != null
              ? Map<String, dynamic>.from(
                  catalogHit['values'] as Map? ?? const {},
                )
              : <String, dynamic>{};
      _markWorkspaceDirty('命中属性节点已新增到内存工作区。');
    });
    return id;
  }

  Map<String, dynamic> remapFor(String state) {
    final per = remaps['${weapon!['id']}'];
    final value = per is Map ? per[state] : null;
    return value is Map ? Map<String, dynamic>.from(value) : const {};
  }

  /// 复用模板：把另一把武器某个状态的动作与命中属性复制到本段。
  Future<void> remapFromTemplate(String stateKey) async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
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
    if (picked == null || !_sameWeaponContext(generation, id, profile, hash)) return;
    final resolved = Map<String, dynamic>.from(
      await widget.api({
        'operation': 'weapon_template_resolve',
        'template_weapon': picked['weapon'],
        'template_stage': picked['state'],
        ..._profileContextFor(profile, hash),
      }),
    );
    if (!_sameWeaponContext(generation, id, profile, hash)) return;
    _validateProfileResponse(resolved, profile, hash);
    final value = <String, dynamic>{
      ...resolved,
      'template_state': '${picked['state']}',
      'allocate_property': true,
    };
    final templateStage = resolved['template_stage_data'];
    if (templateStage is Map) {
      final stage = Map<String, dynamic>.from(
        jsonDecode(jsonEncode(templateStage)) as Map,
      );
      final propertyMap = <String, String>{};
      for (final rawHit in (stage['hits'] as List? ?? const [])) {
        final hit = Map<String, dynamic>.from(rawHit as Map);
        final sourceID = '${hit['id'] ?? ''}'.trim();
        if (sourceID.isEmpty) continue;
        final allocated = _allocateTemplateProperty(sourceID, hit);
        propertyMap[sourceID] = allocated;
        hit['id'] = allocated;
        stage['hits'] = [
          for (final existing in (stage['hits'] as List? ?? const []))
            '${(existing as Map)['id']}' == sourceID ? hit : existing,
        ];
      }
      for (final rawID in (stage['property_ids'] as List? ?? const [])) {
        final sourceID = '$rawID'.trim();
        if (sourceID.isEmpty || propertyMap.containsKey(sourceID)) continue;
        final allocated = _allocateTemplateProperty(
          sourceID,
          {'id': sourceID, 'values': const <String, dynamic>{}},
        );
        propertyMap[sourceID] = allocated;
      }
      if (propertyMap.isNotEmpty) {
        stage['property_ids'] = [
          for (final rawID in (stage['property_ids'] as List? ?? const []))
            propertyMap['$rawID'] ?? '$rawID',
        ];
        value['property_map'] = propertyMap;
        final sourceProperty = '${resolved['property_id'] ?? ''}';
        final allocatedPrimary = propertyMap[sourceProperty];
        if (allocatedPrimary != null) {
          value['property_id'] = allocatedPrimary;
          value['template_property_id'] = sourceProperty;
          final primary = (stage['hits'] as List? ?? const [])
              .where((raw) => '${(raw as Map)['id']}' == allocatedPrimary);
          if (primary.isNotEmpty) value['hit'] = primary.first;
        }
        value['template_stage_data'] = stage;
        value['allocate_property'] = false;
      }
    }
    commitRemap(stateKey, value);
  }

  /// 新增命中属性节点：克隆一个模板节点到全新编号，再指定给本段。
  Future<void> addPropertyFor(String stateKey) async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    Map<String, dynamic> catalog;
    try {
      catalog = Map<String, dynamic>.from(
        await widget.api({
          'operation': 'weapon_remap_options',
          ..._profileContextFor(profile, hash),
        }),
      );
    } catch (_) {
      return;
    }
    if (!_sameWeaponContext(generation, id, profile, hash)) return;
    _validateProfileResponse(catalog, profile, hash);
    final properties = [
      for (final p in (catalog['properties'] as List? ?? []))
        Map<String, dynamic>.from(p as Map),
    ];
    final template = await showDialog<String>(
      context: context,
      builder: (_) => _PropertyPickerDialog(properties: properties),
    );
    if (template == null || !_sameWeaponContext(generation, id, profile, hash)) return;
    final newId = allocateProperty(template, properties);
    final source = hitProperties[template];
    final templateRow = properties.firstWhere((row) => '${row['id']}' == template);
    remaps['${weapon!['id']}'] = {
      ..._stateMap(remaps),
      stateKey: {
        ...remapFor(stateKey),
        'property_id': newId,
        'template_property_id': template,
        'hit': {
          'id': newId,
          'buff': templateRow['buff'] ?? '0',
          'values': source is Map
              ? Map<String, dynamic>.from(source)
              : Map<String, dynamic>.from(
                  templateRow['values'] as Map? ?? const <String, dynamic>{},
                ),
        },
      },
    };
    _markWorkspaceDirty('命中属性 ID 已分配；提交重映射后生成可编辑节点。');
    setState(() {});
  }

  Future<void> clearRemap(String stateKey) async {
    commitRemap(stateKey, const {});
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
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
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
    if (picked == null || !_sameWeaponContext(generation, id, profile, hash)) return;
    setState(() => addStatePick = picked);
    await defineState();
  }

  /// 为自建武器定义一个新状态：选一个尚未使用的状态列，再填动作 / 命中属性 /
  /// 动作说明（可从其它武器复用）。提交后该状态出现在招式列表里。
  Future<void> defineState() async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
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
              isCurrent: () => _sameWeaponContext(generation, id, profile, hash),
              api: _apiForWeaponContext(generation, id, profile, hash),
              weapons: [
                for (final w in (data?['weapons'] as List? ?? []))
                  Map<String, dynamic>.from(w as Map),
              ],
              onCommit: (value) {
                if (!_sameWeaponContext(generation, id, profile, hash)) {
                  throw StateError('武器或客户端配置档已切换，请重新打开编辑器');
                }
                commitRemap(stateKey, value);
              },
              onAddProperty: (template, properties) {
                if (!_sameWeaponContext(generation, id, profile, hash)) {
                  throw StateError('武器或客户端配置档已切换，请重新打开编辑器');
                }
                return allocateProperty(template, properties);
              },
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
    if (!_sameWeaponContext(generation, id, profile, hash)) return;
    setState(() => addStatePick = null);
  }

  /// 删除自建武器的一个状态：清零该动作列，移除重映射与已保存的效果编辑，
  /// 并清掉连招链中涉及该状态的转移。
  Future<void> clearState(String stateKey) async {
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final hash = sourceHash;
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
    if (confirmed != true || !_sameWeaponContext(generation, id, profile, hash)) return;
    setState(() {
      final key = '${weapon!['id']}';
      remaps[key] = _stateMap(remaps)..remove(stateKey);
      cleared[key] = _stateMap(cleared)..[stateKey] = true;
      _clearStateEdits(stateKey);
      final stages = List<dynamic>.from(weapon!['stages'] as List);
      final removed = stages.where((s) => '${s['state']}' == stateKey).toList();
      final removedIDs = {
        for (final s in removed)
          for (final id in (s['property_ids'] as List? ?? const [])) '$id',
      };
      final retainedIDs = {
        for (final s in stages)
          if ('${s['state']}' != stateKey)
            for (final id in (s['property_ids'] as List? ?? const [])) '$id',
      };
      removedIDs.removeAll(retainedIDs);
      bool orphanProperty(Map row) => [
        'skill',
        'prev',
        'cur',
        'exceed_skill_pro_id',
      ].any((field) => removedIDs.contains('${row[field]}'));
      comboRuleMaxDraft.removeWhere(orphanProperty);
      comboRuleBlackDraft.removeWhere(orphanProperty);
      comboRuleWhiteDraft.removeWhere(orphanProperty);
      rules.removeWhere((r) => removed.any((s) => s['stage'] == r['stage']));
      stages.removeWhere((s) => '${s['state']}' == stateKey);
      weapon = {...weapon!, 'stages': stages};
      bool touches(Map row) =>
          [
            'old',
            'new',
            'state',
            'nextstate',
            'next',
          ].any((field) => '${row[field]}' == stateKey) ||
          (row['attrs'] as List? ?? const []).any(
            (a) =>
                ['nextstate', 'next'].contains(a['key']) &&
                '${a['value']}' == stateKey,
          );
      comboChain.removeWhere(touches);
      comboDeadEnds.removeWhere(touches);
      chainDraft.removeWhere(touches);
      frameSwitches.removeWhere(touches);
      counters.removeWhere(touches);
      for (final groups in [frameSaved, frameEdits]) {
        for (final rows in groups.values) {
          rows.removeWhere(touches);
        }
      }
      for (final groups in [counterSaved, counterEdits]) {
        for (final entry in groups.entries.toList()) {
          if (entry.value != null && touches(entry.value!))
            groups[entry.key] = null;
        }
      }
      comboRuleMaxDraft.removeWhere((r) => '${r['exceed_state']}' == stateKey);
      final savedLimits = Map<String, dynamic>.from(
        comboRuleInfo['rules'] as Map? ?? const {},
      );
      for (final group in ['max', 'black', 'white']) {
        savedLimits[group] = [
          for (final row in (savedLimits[group] as List? ?? const []))
            if (!orphanProperty(row as Map) &&
                (group != 'max' || '${row['exceed_state']}' != stateKey))
              row,
        ];
      }
      comboRuleInfo = {...comboRuleInfo, 'rules': savedLimits};
      selectedAction = null;
      editorVersion++;
      _markWorkspaceDirty('状态已从内存工作区删除；点击“保存方案”提交暂存。');
    });
  }

  /// 自建武器的状态管理卡片：新增状态列（从零定义）与提示。
  Widget stateBuilderCard() {
    if (weapon == null || !canEdit(weapon!['id'])) {
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

  /// 重映射失效告警：某个状态的映射指向了不存在的动作或命中属性，此时招式
  /// 列表退回未映射前的结构，需要先修好或取消该映射。
  /// 招架提示：某个状态的 `<Counter>` 指向本武器不存在的状态 —— 架住之后切不到
  /// 任何动作（"白架"）。**只提示、不拦截**：这一招没反应而已，不影响其它招式，
  /// 也不阻止「应用到游戏」。
  Widget counterWarningBanner() {
    final warnings = [
      for (final w in (data?['counter_warnings'] as List? ?? [])) '$w',
    ];
    if (warnings.isEmpty) return const SizedBox.shrink();
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
                Icon(
                  Icons.error_outline,
                  size: 16,
                  color: Colors.deepOrange.shade700,
                ),
                const SizedBox(width: 6),
                Text(
                  '招架目标状态为空（只影响那一招）',
                  style: TextStyle(
                    fontWeight: FontWeight.bold,
                    color: Colors.deepOrange.shade900,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 4),
            for (final line in warnings)
              Padding(
                padding: const EdgeInsets.only(left: 22, top: 2),
                child: Text('· $line', style: const TextStyle(fontSize: 12)),
              ),
            const SizedBox(height: 4),
            const Text(
              '招架写在动画块的 <Counter> 里，GM 只读显示 —— 要改目标状态得手改动作块。',
              style: TextStyle(fontSize: 11, color: Colors.black54),
            ),
          ],
        ),
      ),
    );
  }

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
                Icon(
                  Icons.error_outline,
                  size: 16,
                  color: Colors.deepOrange.shade700,
                ),
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
    final generation = _selectionGeneration;
    final id = weapon!['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    final stateKey = '${stage['state']}';
    return _RemapEditor(
      key: ValueKey('remap-$editorVersion-$generation-$id-$profile-$hash-$stateKey'),
      stateKey: stateKey,
      weaponId: weapon!['id'] as int,
      initial: remapFor(stateKey),
      busy: busy,
      isCurrent: () => _sameWeaponContext(generation, id, profile, hash),
      api: _apiForWeaponContext(generation, id, profile, hash),
      weapons: [
        for (final w in (data?['weapons'] as List? ?? []))
          Map<String, dynamic>.from(w as Map),
      ],
      onCommit: (value) {
        if (!_sameWeaponContext(generation, id, profile, hash)) {
          throw StateError('武器或客户端配置档已切换，请重新打开编辑器');
        }
        commitRemap(stateKey, value);
      },
      onAddProperty: (template, properties) {
        if (!_sameWeaponContext(generation, id, profile, hash)) {
          throw StateError('武器或客户端配置档已切换，请重新打开编辑器');
        }
        return allocateProperty(template, properties);
      },
      onSaved: () async {},
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
            if (stage['counter'] is Map)
              counterLine(Map<String, dynamic>.from(stage['counter'] as Map)),
            if (stage['supported'] != true)
              Text(
                stage['reason'],
                style: const TextStyle(color: Colors.deepOrange),
              ),
            if (stage['notice'] != null && '${stage['notice']}'.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 2),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Icon(
                      Icons.info_outline,
                      size: 14,
                      color: Colors.amber.shade800,
                    ),
                    const SizedBox(width: 4),
                    Expanded(
                      child: Text(
                        '${stage['notice']}',
                        style: TextStyle(
                          fontSize: 12,
                          color: Colors.amber.shade900,
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            const SizedBox(height: 8),
            remapSection(index, Map<String, dynamic>.from(stage as Map)),
            if (canEdit(weapon!['id']))
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
                  child: TextFormField(
                    key: ValueKey('$editorVersion-${rule['stage']}-level'),
                    initialValue: _levelText(rule['level']),
                    enabled: enabled && rule['buff'] != 0,
                    decoration: const InputDecoration(labelText: '等级/倍率'),
                    keyboardType: const TextInputType.numberWithOptions(
                      decimal: true,
                    ),
                    validator: (v) {
                      final n = double.tryParse(v ?? '');
                      return n == null || n < 0.1 || n > 999
                          ? '请输入 0.1–999（如 0.25）'
                          : null;
                    },
                    onChanged: (v) => setState(() {
                      rule['level'] = double.tryParse(v) ?? 0;
                      dirty = true;
                    }),
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
    if (index == null ||
        index < 0 ||
        index >= (weapon!['stages'] as List).length) {
      return '';
    }
    final stage = weapon!['stages'][index];
    final ids = (stage['property_ids'] as List? ?? []).join('、');
    final frames = (stage['frames'] as num?)?.toInt() ?? 0;
    final raw = (stage['raw_frames'] as num?)?.toInt() ?? 0;
    final clip = '${stage['clip'] ?? ''}';
    // 动作块几乎都带一条 endframe=99/999 的尾巴片断（保持姿势到被中断），
    // 它只说明"尾巴"，不是真实时长。真实时长取引用动画 .anm 自己的帧数；
    // 读不到动画时才退回动作块的最大 endframe，并标明是估算。
    final label = frames == 0
        ? (raw == 0 ? '' : ' · 总帧数 $raw（动作块估算）')
        : ' · 总帧数 $frames${clip.isEmpty ? '' : ' · 动画 $clip'}';
    return '状态 ${stage['state']} · 动作 ${stage['action']}$label'
        '${ids.isEmpty ? '' : ' · 命中属性 $ids'}';
  }

  /// 招架（动画块里的 `<Counter>`）：**被对手攻击触发**的第三条转移通道 ——
  /// 连招链（delayacttable）与帧连招（CustomStateSwitch）里都看不到它。
  /// GM 只读显示：想改窗口/目标状态要手改动作块的原文。
  Widget counterLine(Map<String, dynamic> counter) {
    final next = '${counter['next_state'] ?? ''}';
    final half = (counter['angle_half_range'] as num?)?.toInt() ?? 0;
    final size = '${counter['trigger_box'] ?? ''}';
    final head =
        '招架：帧 ${counter['start_frame']}–${counter['end_frame']} → 状态 $next'
        '${half == 0 ? '' : '（±$half°）'}${size.isEmpty ? '' : ' · 判定盒 $size'}';
    final dead = counter['next_unreachable'] == true;
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.only(top: 4),
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 5),
      decoration: BoxDecoration(
        color: dead ? Colors.red.shade50 : Colors.orange.shade50,
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(
        dead
            ? '$head —— 该武器没有这个状态，架住后切不到任何动作（只影响这一招）'
            : '$head · 被对手攻击打中时触发（只读）',
        style: TextStyle(
          fontSize: 12,
          color: dead ? Colors.red.shade900 : Colors.orange.shade900,
        ),
      ),
    );
  }

  Widget actionChoice(String key, int? index, String label) {
    // 自建武器的每条动作都可以直接删掉（= 删除该状态列），不用先点进编辑卡。
    final deletable = index != null && canEdit(weapon!['id']);
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
                                '${(weapon!['stages'][index!] as Map)['state']}',
                              ),
                      ),
                    IconButton(
                      tooltip: '帧轨道与攻击范围',
                      icon: const Icon(Icons.timeline, size: 18),
                      onPressed: busy ? null : () => openStageTrack(index!),
                    ),
                    IconButton(
                      tooltip: '招式特效预览与编辑',
                      icon: const Icon(Icons.auto_fix_high, size: 18),
                      onPressed: busy ? null : () => openStageEffects(index!),
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

  late final itemPictures = ItemPictures(widget.api);
  Widget weaponIcon(Map<dynamic, dynamic> value, double size) => itemPictures
      .preview({'key': '25:${value['id']}', 'name': value['name']}, size: size);

  /// Page-level strip: which client this editor works on and its full
  /// config.spf2 SHA-256. Only the current client's id is shown — comparing it
  /// against the server's stored value was noise.
  Widget clientStrip() {
    if (_clientInfo.isEmpty) return const SizedBox.shrink();
    final directory = '${_clientInfo['directory'] ?? ''}';
    final hash = clientConfigHash;
    final baselineState = '${(data?['client'] as Map?)?['state'] ?? ''}';
    final rebaseNeeded =
        baselineState == '有差异' ||
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
                if ('${_clientInfo['source'] ?? ''}'.isNotEmpty) ...[
                  const SizedBox(width: 8),
                  Text(
                    '来自 ${_clientInfo['source']}',
                    style: TextStyle(
                      fontSize: 11,
                      color: Theme.of(context).hintColor,
                    ),
                  ),
                ],
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
                    child: const Text('重新采集基线', style: TextStyle(fontSize: 12)),
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
                    style: const TextStyle(
                      fontSize: 11,
                      fontFamily: 'monospace',
                    ),
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
                      '点「应用到游戏」才会写入客户端；武器包导入先进入临时配置。',
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
    final generation = _selectionGeneration;
    final id = weapon?['id'];
    final profile = activeProfile;
    final contextHash = sourceHash;
    await Clipboard.setData(ClipboardData(text: hash));
    if (_sameWeaponContext(generation, id, profile, contextHash)) {
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
                          : rows == 0
                          ? '参考武器「${weaponName(reference)}」没有连招表，未复制连招；它有的动作特效等登记仍会照常复制'
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

  /// 非自建武器默认只读；点「启用编辑」后本次选中期间开放全部编辑按钮，
  /// 重新选中武器即复位。
  Widget editUnlockBanner() {
    if (weapon == null || isCreated(weapon!['id']) || editUnlocked) {
      return const SizedBox.shrink();
    }
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Card(
        color: Colors.amber.shade50,
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Row(
            children: [
              const Icon(
                Icons.lock_open_outlined,
                size: 18,
                color: Colors.deepOrange,
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  '「${weapon!['name']}」不是编辑器登记的自建武器。'
                  '启用编辑后，本次会开放全部招式、连招与特效编辑；'
                  '重新选中武器后会恢复只读。',
                  style: const TextStyle(fontSize: 12),
                ),
              ),
              const SizedBox(width: 8),
              FilledButton.icon(
                onPressed: busy
                    ? null
                    : () => setState(() => editUnlocked = true),
                icon: const Icon(Icons.edit, size: 16),
                label: const Text('启用编辑'),
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
                                          setState(() => message = '');
                                          await select(
                                            Map<String, dynamic>.from(value),
                                          );
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
                        ? Center(
                            child: Text(
                              busy
                                  ? '正在读取武器配置…'
                                  : allWeapons.isEmpty
                                  ? '没有可读取的武器'
                                  : '请从左侧选择武器查看配置',
                            ),
                          )
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
                                                          style: Theme.of(
                                                            context,
                                                          ).textTheme.bodySmall,
                                                          overflow: TextOverflow
                                                              .ellipsis,
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
                                          const SizedBox(width: 12),
                                          Column(
                                            mainAxisAlignment:
                                                MainAxisAlignment.center,
                                            children: [
                                              OutlinedButton.icon(
                                                onPressed: busy
                                                    ? null
                                                    : openEffectEditor,
                                                icon: const Icon(
                                                  Icons.auto_fix_high,
                                                  size: 16,
                                                ),
                                                label: const Text('模型特效'),
                                              ),
                                              const SizedBox(height: 4),
                                              Text(
                                                '装备时预加载',
                                                style: TextStyle(
                                                  fontSize: 11,
                                                  color: Theme.of(context)
                                                      .hintColor,
                                                ),
                                              ),
                                            ],
                                          ),
                                        ],
                                      ),
                                    ),
                                  ),
                                ),
                                const SizedBox(height: 8),
                                comboBanner(),
                                editUnlockBanner(),
                                remapErrorBanner(),
                                counterWarningBanner(),
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
                                              counterCard(),
                                              blockElementCard(),
                                              variantCard(),
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
                                                if (canEdit(weapon!['id']))
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
                                            if ((weapon!['combos'] as List? ??
                                                    [])
                                                .isEmpty)
                                              const Text(
                                                '未收录按键提示，按动作名称选择。',
                                                style: TextStyle(fontSize: 12),
                                              ),
                                            if (weapon!['id'] == 253013)
                                              Align(
                                                alignment: Alignment.centerLeft,
                                                child: TextButton.icon(
                                                  onPressed: busy
                                                      ? null
                                                      : () => setState(() {
                                                          editorVersion++;
                                                          for (final r
                                                              in rules) {
                                                            r['buff'] =
                                                                r['stage'] == 1
                                                                ? 1
                                                                : r['stage'] ==
                                                                      2
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
                                      onPressed: busy || dirty
                                          ? null
                                          : repairEffects,
                                      icon: const Icon(Icons.auto_fix_high),
                                      label: const Text('自动补齐攻击特效'),
                                    ),
                                    OutlinedButton.icon(
                                      onPressed: busy
                                          ? null
                                          : () => execute('weapon_save'),
                                      icon: const Icon(Icons.save_outlined),
                                      label: const Text('保存方案'),
                                    ),
                                    OutlinedButton.icon(
                                      onPressed: busy ? null : loadWorkspace,
                                      icon: const Icon(Icons.restore),
                                      label: const Text('加载暂存'),
                                    ),
                                    OutlinedButton.icon(
                                      onPressed: busy || !workspaceExists
                                          ? null
                                          : deleteWorkspace,
                                      icon: const Icon(Icons.delete_outline),
                                      label: const Text('删除暂存'),
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
                                    if (isCreated(weapon!['id']) &&
                                        blueprintOf(
                                              weapon!['id'],
                                            )['recovered'] !=
                                            true)
                                      TextButton(
                                        onPressed: busy ? null : forget,
                                        child: const Text('移除自建武器'),
                                      ),
                                    OutlinedButton.icon(
                                      onPressed: busy ? null : exportPackage,
                                      icon: const Icon(
                                        Icons.inventory_2_outlined,
                                      ),
                                      label: const Text('导出发版包'),
                                    ),
                                    OutlinedButton.icon(
                                      onPressed: busy
                                          ? null
                                          : exportMergePackage,
                                      icon: const Icon(Icons.call_merge),
                                      label: const Text('导出合并包'),
                                    ),
                                    OutlinedButton.icon(
                                      onPressed: busy
                                          ? null
                                          : importMergePackage,
                                      icon: const Icon(
                                        Icons.file_upload_outlined,
                                      ),
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

  // ---------------------------------------------------------------------------
  // 帧轨道与攻击范围
  // ---------------------------------------------------------------------------

  /// 拉取这把武器的帧轨道（一次拿全部状态），再打开某个招式的弹窗。
  /// 轨道是从动作块现读的，所以弹窗里能看到刚保存的编辑。
  Future<void> openStageTrack(int index) async {
    if (busy || weapon == null) return;
    final generation = _selectionGeneration;
    final id = weapon!['id'];
    final profile = activeProfile;
    final hash = sourceHash;
    final stages = weapon!['stages'] as List;
    if (index < 0 || index >= stages.length) return;
    final stage = Map<String, dynamic>.from(stages[index] as Map);
    final state = '${stage['state']}';
    setState(() {
      busy = true;
      message = '正在读取帧轨道…';
    });
    try {
      final result = Map<String, dynamic>.from(
        await widget.api({
          'operation': 'weapon_stage_track',
          'weapon': id,
          ..._profileContextFor(profile, hash),
        }),
      );
      if (!_sameWeaponContext(generation, id, profile, hash)) return;
      _validateProfileResponse(result, profile, hash);
      final tracks = <String, Map<String, dynamic>>{
        for (final t in (result['tracks'] as List? ?? []))
          '${(t as Map)['state']}': Map<String, dynamic>.from(t),
      };
      final saved = <String, Map<String, List<Map<String, dynamic>>>>{
        for (final e in (result['saved'] as Map? ?? {}).entries)
          '${e.key}': {
            for (final s in (e.value as Map).entries)
              '${s.key}': [
                for (final a in (s.value as List))
                  Map<String, dynamic>.from(a as Map),
              ],
          },
      };
      setState(() {
        stageTracks = tracks;
        scopeSaved = saved;
        busy = false;
        message = '';
      });
      final track = tracks[state];
      if (track == null) {
        setState(() => message = '后端没有返回状态 $state 的轨道，请更新并重新编译管理后端。');
        return;
      }
      final changed = await showDialog<Map<String, List<Map<String, dynamic>>>>(
        context: context,
        builder: (_) => _StageTrackDialog(
          api: _apiForWeaponContext(generation, id, profile, hash),
          weapon: id as int,
          stage: stage,
          track: track,
          saved: saved[state] ?? const {},
          ustates: ustateOptions,
        ),
      );
      if (changed != null && _sameWeaponContext(generation, id, profile, hash)) {
        setState(() {
          final stateScopes = <String, List<Map<String, dynamic>>>{
            ...?scopeSaved[state],
          };
          for (final entry in changed.entries) {
            stateScopes[entry.key] = [
              for (final attr in entry.value) Map<String, dynamic>.from(attr),
            ];
          }
          scopeSaved[state] = stateScopes;
          stageTracks = {...stageTracks};
          _markWorkspaceDirty('攻击范围已更新到当前内存工作区；点击“保存方案”提交暂存。');
        });
      }
    } catch (e) {
      if (_sameWeaponContext(generation, id, profile, hash)) {
        setState(() {
          busy = false;
          message = '帧轨道读取失败：$e';
        });
      }
    }
  }
}

/// 招式「帧轨道 + 攻击范围」弹窗。左边是帧轨道（动作片断 + 特效/音效/接招窗口
/// 等标记），右边编辑所选片断的攻击范围盒，并与人物受击盒一起预览（顶视 / 侧视）。
///
/// 保存只返回当前弹窗的攻击范围编辑集，由父页面并入 WeaponWorkspace。
class _StageTrackDialog extends StatefulWidget {
  const _StageTrackDialog({
    required this.api,
    required this.weapon,
    required this.stage,
    required this.track,
    required this.saved,
    required this.ustates,
  });

  final Future<dynamic> Function(Map<String, dynamic>) api;
  final int weapon;
  final Map<String, dynamic> stage;
  final Map<String, dynamic> track;
  final Map<String, List<Map<String, dynamic>>> saved;

  /// 状态号 → 名称（用于把片断的触发条件显示成「406（黑暗武器）」）。
  final List<Map<String, String>> ustates;

  @override
  State<_StageTrackDialog> createState() => _StageTrackDialogState();
}

/// 攻击范围 / 人物盒的六个属性，按「先位置后尺寸」排。
const scopeFields = <String, String>{
  'centerx': '左右 x',
  'centery': '高低 y',
  'centerz': '前方 z',
  'length': '纵深（长）',
  'width': '宽度（宽）',
  'heigth': '高度（高）',
};

/// 动作片断没有自带攻击范围时新建盒子的默认值（照普通轻攻击填）。
const scopeDefaults = <String, String>{
  'centerx': '0',
  'centery': '15',
  'centerz': '10',
  'length': '20',
  'width': '20',
  'heigth': '20',
};

/// 读一组属性里的某个键。
String? scopeValueOf(List? attrs, String key) {
  for (final attr in (attrs ?? const [])) {
    if ('${(attr as Map)['key']}' == key) return '${attr['value']}';
  }
  return null;
}

/// 片断在「同一招式」里的唯一键。同一份 AnmDesc id 允许注册多条（一条无条件 +
/// 若干带 <Condition>），两块里的 <Anm id> 各自独立编号、必然冲突，所以光用 id
/// 会在列表/轨道里串行。带条件的片断用 `id#cond<状态号>` 区分。
String stageSegmentKey(Map segment) {
  final id = '${segment['id']}';
  final cond = '${segment['condition'] ?? ''}';
  return cond.isEmpty ? id : '$id#cond$cond';
}

class _StageTrackDialogState extends State<_StageTrackDialog> {
  late List<Map<String, dynamic>> segments;
  late List<Map<String, dynamic>> markers;
  late int frames;
  String? selected;

  /// 片断 → 六个属性（字符串）；saved 与块里自带的值合并后作为初值。
  final edits = <String, Map<String, String>>{};

  /// 真正改过的片断，只有这些会被提交（免得"打开就多出一个盒"）。
  final touched = <String>{};
  final defaults = <String, Map<String, String>>{};
  bool side = false;
  bool busy = false;
  String message = '';

  @override
  void initState() {
    super.initState();
    segments = [
      for (final s in (widget.track['segments'] as List? ?? []))
        Map<String, dynamic>.from(s as Map),
    ];
    markers = [
      for (final m in (widget.track['markers'] as List? ?? []))
        Map<String, dynamic>.from(m as Map),
    ];
    frames = (widget.track['frames'] as num?)?.toInt() ?? 0;
    for (final segment in segments) {
      final key = stageSegmentKey(segment);
      final cond = '${segment['condition'] ?? ''}';
      final block = segment['scope'] as List? ?? const [];
      // 已保存的攻击范围只有「无条件」那一条能写回（后端只改动作块的第一份），
      // 条件分支的攻击范围目前不可保存，所以不给它套用已保存值。
      final savedAttrs = cond.isEmpty ? widget.saved['${segment['id']}'] : null;
      defaults[key] = {
        for (final field in scopeFields.keys)
          field: scopeValueOf(block, field) ?? scopeDefaults[field]!,
      };
      edits[key] = {
        for (final field in scopeFields.keys)
          field: scopeValueOf(savedAttrs, field) ?? defaults[key]![field]!,
      };
      if (savedAttrs != null && savedAttrs.isNotEmpty) touched.add(key);
    }
    selected = _firstWithScope();
  }

  /// 某个片断是否属于条件分支（`<Condition>` 分流出来的那几条）。条件分支的
  /// 攻击范围目前只能看、不能保存，所以编辑器要区别对待。
  bool segmentConditional(Map segment) =>
      '${segment['condition'] ?? ''}'.isNotEmpty;

  /// 条件号 → 「406（黑暗武器）」，没收录就只显示编号。
  String conditionLabel(String id) {
    for (final u in widget.ustates) {
      if (u['id'] == id && (u['name'] ?? '').isNotEmpty) {
        return '$id（${u['name']}）';
      }
    }
    return id;
  }

  String? _firstWithScope() {
    for (final segment in segments) {
      if ((segment['scope'] as List? ?? []).isNotEmpty) {
        return stageSegmentKey(segment);
      }
    }
    return segments.isEmpty ? null : stageSegmentKey(segments.first);
  }

  /// 本招式里"不属于某个动画片断"的盒子：力场盒（`<ForceField><ScopeBox>`）、
  /// 投技盒（`<LockedAttackSwitch><Scope>`）。它们按帧生效，所以带上帧窗口。
  List<PreviewBox> get extraBoxes {
    final result = <PreviewBox>[];
    for (final raw in (widget.track['boxes'] as List? ?? [])) {
      final box = Map<String, dynamic>.from(raw as Map);
      final attrs = <String, String>{
        for (final a in (box['attrs'] as List? ?? []))
          '${(a as Map)['key']}': '${a['value']}',
      };
      if (attrs.isEmpty) continue;
      final kind = '${box['kind']}';
      final start = (box['start'] as num?)?.toInt() ?? 0;
      final end = (box['end'] as num?)?.toInt() ?? 0;
      final window = kind == 'force' && (start != 0 || end != 0)
          ? '帧 $start–$end'
          : '';
      result.add(
        PreviewBox(
          '${box['label'] ?? kind}',
          attrs,
          kind == 'grab' ? const Color(0xFF7E57C2) : const Color(0xFFE07B39),
          window: window,
        ),
      );
    }
    return result;
  }

  Widget _boxChip(String text, Color color) => Row(
    mainAxisSize: MainAxisSize.min,
    children: [
      Container(
        width: 10,
        height: 10,
        decoration: BoxDecoration(
          color: color.withValues(alpha: .25),
          border: Border.all(color: color, width: 1.2),
          borderRadius: BorderRadius.circular(2),
        ),
      ),
      const SizedBox(width: 4),
      Text(text, style: const TextStyle(fontSize: 11)),
    ],
  );

  /// 盒子的六个数值，紧挨在预览下面，方便对着图核对。
  Widget boxReadout(String label, Map<String, String> attrs) {
    String num(String key) => attrs[key] ?? '0';
    return Padding(
      padding: const EdgeInsets.only(bottom: 2),
      child: Text(
        '$label：中心 (${num('centerx')}, ${num('centery')}, ${num('centerz')})'
        ' · 长 ${num('length')} 宽 ${num('width')} 高 ${num('heigth')}',
        style: const TextStyle(fontSize: 11, fontFamily: 'monospace'),
      ),
    );
  }

  /// 预览用的人物受击盒：所选片断自带 CharScope 就照它画，否则用默认身位。
  /// 很多片断把 CharScope 写成全 0（等于没写），这种也退回默认身位。
  Map<String, String> get charBox {
    final id = selected;
    if (id != null) {
      for (final segment in segments) {
        if (stageSegmentKey(segment) != id) continue;
        final box = segment['char_scope'] as List? ?? const [];
        if (box.isEmpty) break;
        final merged = Map<String, String>.from(defaultCharBox);
        for (final key in scopeFields.keys) {
          final value = scopeValueOf(box, key);
          if (value != null) merged[key] = value;
        }
        final length = double.tryParse(merged['length'] ?? '') ?? 0;
        final width = double.tryParse(merged['width'] ?? '') ?? 0;
        final heigth = double.tryParse(merged['heigth'] ?? '') ?? 0;
        if (length == 0 && width == 0 && heigth == 0) break;
        return merged;
      }
    }
    return defaultCharBox;
  }

  Map<String, dynamic>? _segmentByKey(String key) {
    for (final segment in segments) {
      if (stageSegmentKey(segment) == key) return segment;
    }
    return null;
  }

  Future<void> save() async {
    final payload = <String, List<Map<String, dynamic>>>{};
    for (final key in touched) {
      final segment = _segmentByKey(key);
      if (segment == null || segmentConditional(segment)) continue;
      payload['${segment['id']}'] = [
        for (final entry in scopeFields.entries)
          {'key': entry.key, 'value': edits[key]![entry.key]!},
      ];
    }
    if (payload.isEmpty) {
      setState(() => message = '还没有改过任何片断。');
      return;
    }
    closeDialog(payload);
  }

  void closeDialog([Map<String, List<Map<String, dynamic>>>? result]) {
    if (!mounted) return;
    Navigator.of(context, rootNavigator: true).pop(result);
  }

  @override
  Widget build(BuildContext context) {
    final stage = widget.stage;
    return PopScope<Map<String, List<Map<String, dynamic>>>>(
      canPop: !busy,
      onPopInvokedWithResult: (didPop, result) {
        if (!didPop && !busy) closeDialog(result);
      },
      child: AlertDialog(
        title: Row(
          children: [
            const Icon(Icons.timeline, size: 18),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                '帧轨道与攻击范围 · 状态 ${stage['state']} · ${stage['label'] ?? ''}',
                style: const TextStyle(fontSize: 15),
              ),
            ),
            IconButton(
              tooltip: '关闭帧轨道与攻击范围',
              visualDensity: VisualDensity.compact,
              onPressed: busy ? null : () => closeDialog(),
              icon: const Icon(Icons.close, size: 18),
            ),
          ],
        ),
      contentPadding: const EdgeInsets.fromLTRB(20, 6, 20, 0),
      content: SizedBox(
        width: 920,
        height: 540,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  SizedBox(width: 470, child: trackPane()),
                  const VerticalDivider(width: 24),
                  Expanded(child: scopePane()),
                ],
              ),
            ),
            if (message.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 6, bottom: 2),
                child: Text(
                  message,
                  style: TextStyle(
                    fontSize: 12,
                    color: message.startsWith('保存失败') ? Colors.red : null,
                  ),
                ),
              ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: busy ? null : () => closeDialog(),
          child: const Text('关闭'),
        ),
        FilledButton(
          onPressed: busy ? null : save,
          child: Text(busy ? '保存中…' : '保存攻击范围'),
        ),
      ],
      ),
    );
  }

  // ------------------------------------------------------------------ 轨道

  Widget trackPane() {
    final clip = '${widget.track['clip'] ?? ''}';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          '总帧数 $frames${clip.isEmpty ? '' : ' · 主动画 $clip'}'
          '${segments.isEmpty ? '（动作块里没有 <Anm> 片断）' : ''}',
          style: const TextStyle(fontSize: 12, fontWeight: FontWeight.bold),
        ),
        const SizedBox(height: 2),
        const Text(
          '动作块末尾常见一条 endframe=99/999 的片断，那只是"保持姿势到被中断"，'
          '不是真实时长；这里按引用动画自己的帧数标尺画。',
          style: TextStyle(fontSize: 11, color: Colors.black54),
        ),
        const SizedBox(height: 8),
        SizedBox(
          height: 30 + segments.length * 20 + 26,
          child: CustomPaint(
            painter: _FrameTrackPainter(
              frames: frames,
              segments: segments,
              markers: markers,
              selected: selected,
            ),
            size: Size.infinite,
          ),
        ),
        const SizedBox(height: 6),
        markerLegend(),
        const SizedBox(height: 6),
        const Text(
          '动作片断（点选后编辑它的攻击范围）',
          style: TextStyle(fontSize: 12, fontWeight: FontWeight.bold),
        ),
        Expanded(
          child: ListView(
            children: [
              for (final segment in segments) segmentTile(segment),
              if (segments.isEmpty)
                const Padding(
                  padding: EdgeInsets.all(8),
                  child: Text('这一招没有动作片断，无法编辑攻击范围。'),
                ),
            ],
          ),
        ),
      ],
    );
  }

  Widget markerLegend() {
    if (markers.isEmpty) {
      return const Text(
        '轨道上没有特效 / 音效 / 接招窗口标记。',
        style: TextStyle(fontSize: 11, color: Colors.black54),
      );
    }
    final kinds = <String, int>{};
    for (final marker in markers) {
      kinds['${marker['kind']}'] = (kinds['${marker['kind']}'] ?? 0) + 1;
    }
    return Wrap(
      spacing: 10,
      runSpacing: 4,
      children: [
        for (final entry in kinds.entries)
          Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Container(
                width: 10,
                height: 10,
                decoration: BoxDecoration(
                  color: _markerColor(entry.key),
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
              const SizedBox(width: 4),
              Text(
                '${_markerName(entry.key)} × ${entry.value}',
                style: const TextStyle(fontSize: 11),
              ),
            ],
          ),
      ],
    );
  }

  Widget segmentTile(Map<String, dynamic> segment) {
    final id = '${segment['id']}';
    final key = stageSegmentKey(segment);
    final cond = '${segment['condition'] ?? ''}';
    final conditional = cond.isNotEmpty;
    final clip = (segment['clip_frames'] as num?)?.toInt() ?? 0;
    final isSelected = key == selected;
    final hasScope = (segment['scope'] as List? ?? []).isNotEmpty;
    return Container(
      key: ValueKey('stage-seg-$key'),
      margin: const EdgeInsets.only(bottom: 4),
      decoration: BoxDecoration(
        color: isSelected ? const Color(0xFFE3F1F2) : null,
        borderRadius: BorderRadius.circular(8),
      ),
      child: ListTile(
        dense: true,
        selected: isSelected,
        leading: Icon(
          conditional
              ? Icons.alt_route
              : (hasScope ? Icons.crop_free : Icons.crop_free_outlined),
          size: 18,
          color: conditional
              ? const Color(0xFFB26A00)
              : (hasScope ? const Color(0xFF087E83) : Colors.grey),
        ),
        title: Row(
          children: [
            Flexible(
              child: Text(
                '片断 $id · ${segment['name'] ?? ''}',
                style: const TextStyle(fontSize: 13),
                overflow: TextOverflow.ellipsis,
              ),
            ),
            if (conditional) ...[
              const SizedBox(width: 6),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
                decoration: BoxDecoration(
                  color: const Color(0xFFFFF1DC),
                  border: Border.all(color: const Color(0xFFE0A850)),
                  borderRadius: BorderRadius.circular(4),
                ),
                child: Text(
                  '条件 · ${conditionLabel(cond)}',
                  style: const TextStyle(
                    fontSize: 10,
                    color: Color(0xFF8A5A00),
                  ),
                ),
              ),
            ],
          ],
        ),
        subtitle: Text(
          '时间轴 ${segment['start']}–${segment['end']}'
          '${clip == 0 ? '' : ' · 动画 $clip 帧'}'
          '${(segment['skillproid'] ?? '').toString().isEmpty ? '' : ' · 命中属性 ${segment['skillproid']}'}'
          '${hasScope ? '' : ' · 原块无攻击范围'}',
          style: const TextStyle(fontSize: 11),
        ),
        trailing: touched.contains(key)
            ? const Icon(Icons.edit, size: 16, color: Color(0xFF087E83))
            : null,
        onTap: () => setState(() => selected = key),
      ),
    );
  }

  // ------------------------------------------------------------ 攻击范围

  Widget scopePane() {
    final id = selected;
    if (id == null) {
      return const Center(child: Text('这一招没有可编辑的动作片断。'));
    }
    final segment = _segmentByKey(id);
    final cond = '${segment?['condition'] ?? ''}';
    final conditional = cond.isNotEmpty;
    final attrs = edits[id]!;
    final scope = <String, String>{
      for (final entry in attrs.entries) entry.key: entry.value,
    };
    return SingleChildScrollView(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            '攻击范围 · 片断 ${segment?['id'] ?? id}'
            '${conditional ? '（条件 ${conditionLabel(cond)}）' : ''}',
            style: const TextStyle(fontSize: 13, fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 6),
          if (conditional) ...[
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(8),
              decoration: BoxDecoration(
                color: const Color(0xFFFFF1DC),
                border: Border.all(color: const Color(0xFFE0A850)),
                borderRadius: BorderRadius.circular(6),
              ),
              child: Text(
                '这段动作只在玩家拥有状态 ${conditionLabel(cond)} 时才成立（动作块头部的 '
                '<Condition>）。它的攻击范围目前只能查看、不能保存 —— 同名的无条件片断'
                '共用同一个 <Anm id>，直接回写会改到无条件片上。',
                style: const TextStyle(fontSize: 11, color: Color(0xFF8A5A00)),
              ),
            ),
            const SizedBox(height: 10),
          ],
          const Text(
            '六个属性就是动作块里的 <AttackScope>：x 左右、y 上下、z 前方，'
            '长/宽/高是盒子的三边。0 表示这条边不占位。',
            style: TextStyle(fontSize: 11, color: Colors.black54),
          ),
          const SizedBox(height: 10),
          for (final entry in scopeFields.entries)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: TextFormField(
                key: ValueKey('scope-$id-${entry.key}'),
                initialValue: attrs[entry.key],
                readOnly: conditional,
                keyboardType: const TextInputType.numberWithOptions(
                  signed: true,
                ),
                decoration: InputDecoration(
                  labelText: '${entry.value}（${entry.key}）',
                  isDense: true,
                ),
                onChanged: conditional
                    ? null
                    : (value) => setState(() {
                        attrs[entry.key] = value.trim().isEmpty
                            ? '0'
                            : value.trim();
                        touched.add(id);
                      }),
              ),
            ),
          const SizedBox(height: 4),
          Row(
            children: [
              const Text('预览：', style: TextStyle(fontSize: 12)),
              const SizedBox(width: 6),
              ChoiceChip(
                label: const Text('顶视（左右×前后）'),
                selected: !side,
                onSelected: (_) => setState(() => side = false),
              ),
              const SizedBox(width: 6),
              ChoiceChip(
                label: const Text('侧视（前后×高低）'),
                selected: side,
                onSelected: (_) => setState(() => side = true),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Center(
            child: Container(
              decoration: BoxDecoration(
                border: Border.all(color: const Color(0xFFD6DEE4)),
                borderRadius: BorderRadius.circular(8),
              ),
              child: CustomPaint(
                painter: _ScopePreviewPainter(
                  charBox: charBox,
                  scope: scope,
                  extras: extraBoxes,
                  side: side,
                ),
                size: const Size(360, 300),
              ),
            ),
          ),
          const SizedBox(height: 6),
          Wrap(
            spacing: 10,
            runSpacing: 4,
            children: [
              _boxChip('人物盒', const Color(0xFF607D8B)),
              _boxChip('攻击盒（正在编辑）', const Color(0xFF087E83)),
              for (final box in extraBoxes)
                _boxChip(
                  box.window.isEmpty ? box.label : '${box.label} ${box.window}',
                  box.color,
                ),
            ],
          ),
          const SizedBox(height: 6),
          const Text(
            '侧视：横轴 Z 前方 / 纵轴 Y 上下；顶视：横轴 X 左右 / 纵轴 Z 前方。'
            '原点 (0,0,0) 在角色脚下，所以 y=0 那条线就是地面；盒子按中心±尺寸/2 画，'
            'y 很低的盒子确实会沉到地面以下。',
            style: TextStyle(fontSize: 11, color: Colors.black54),
          ),
          const SizedBox(height: 6),
          boxReadout('攻击盒', scope),
          boxReadout('人物盒', charBox),
          for (final box in extraBoxes)
            boxReadout(
              box.window.isEmpty ? box.label : '${box.label} ${box.window}',
              box.attrs,
            ),
          const SizedBox(height: 6),
          Row(
            children: [
              TextButton.icon(
                onPressed: () => setState(() {
                  edits[id] = Map<String, String>.from(defaults[id]!);
                  touched.remove(id);
                }),
                icon: const Icon(Icons.undo, size: 16),
                label: const Text('恢复原块值'),
              ),
              if (touched.contains(id))
                Text(
                  '已修改',
                  style: TextStyle(fontSize: 12, color: Colors.orange.shade900),
                ),
            ],
          ),
          const Text(
            '保存只写入编辑集；回到武器页点「应用到游戏」才写进客户端配置包。'
            '共用动作块会自动克隆成该武器独占，不影响其它武器。',
            style: TextStyle(fontSize: 11, color: Colors.black54),
          ),
        ],
      ),
    );
  }
}

/// 预览用的人物身位盒（动作片断没写 CharScope，或写成全 0 时的缺省值）。
/// 取自 CharScope 的众数写法 `centery=15 length=12 width=10 heigth=30`
/// —— 即"脚下为原点、30 高、深 12、宽 10"的标准角色盒子。
const defaultCharBox = <String, String>{
  'centerx': '0',
  'centery': '15',
  'centerz': '0',
  'length': '12',
  'width': '10',
  'heigth': '30',
};

String _markerName(String kind) {
  switch (kind) {
    case 'key':
      return '接招窗口';
    case 'hit':
      return '命中特效';
    case 'effect':
      return '特效';
    case 'audio':
      return '音效';
    case 'guard':
      return '防护';
    case 'counter':
      return '招架';
  }
  return kind;
}

Color _markerColor(String kind) {
  switch (kind) {
    case 'key':
      return const Color(0xFF087E83);
    case 'hit':
      return const Color(0xFFE07B39);
    case 'effect':
      return const Color(0xFF7E57C2);
    case 'audio':
      return const Color(0xFF5C6BC0);
    case 'guard':
      return const Color(0xFF29A3A3);
    case 'counter':
      return const Color(0xFFD9455F);
  }
  return Colors.grey;
}

/// 帧轨道：按"真实帧数"标尺画出每条 <Anm> 片断，并把特效/音效/接招窗口等
/// 标记画成半透明色带。尾巴片断会明显超出"真实时长"标尺，一眼能看出来。
class _FrameTrackPainter extends CustomPainter {
  _FrameTrackPainter({
    required this.frames,
    required this.segments,
    required this.markers,
    required this.selected,
  });

  final int frames;
  final List<Map<String, dynamic>> segments;
  final List<Map<String, dynamic>> markers;
  final String? selected;

  static const _pad = 10.0;

  @override
  void paint(Canvas canvas, Size size) {
    final total = frames <= 0 ? 1 : frames;
    // 即使片断超出真实帧数（尾巴到 99/999），也要能画出来，所以标尺按二者的
    // 较大值铺开，再用一条竖线标出"真实结尾"。
    var span = total;
    for (final segment in segments) {
      final end = (segment['end'] as num?)?.toInt() ?? 0;
      if (end + 1 > span) span = end + 1;
    }
    final width = size.width - 2 * _pad;
    double at(num frame) => _pad + (frame.clamp(0, span) / span) * width;

    final tick = Paint()
      ..color = const Color(0xFFB6C2CB)
      ..strokeWidth = 1;
    final axis = Paint()
      ..color = const Color(0xFF8794A0)
      ..strokeWidth = 1;
    const axisY = 16.0;
    canvas.drawLine(
      const Offset(_pad, axisY),
      Offset(size.width - _pad, axisY),
      axis,
    );
    final step = span <= 40
        ? 5
        : span <= 120
        ? 10
        : span <= 400
        ? 50
        : 100;
    for (var frame = 0; frame <= span; frame += step) {
      final x = at(frame);
      canvas.drawLine(Offset(x, axisY - 4), Offset(x, axisY), tick);
      _label(canvas, '$frame', Offset(x, 1), 9, const Color(0xFF6B7A87), 34);
    }

    // 真实结尾线：动作真正播完的位置。
    final tail = at(total);
    canvas.drawLine(
      Offset(tail, axisY),
      Offset(tail, size.height - 4),
      Paint()
        ..color = const Color(0xFF087E83)
        ..strokeWidth = 1.4,
    );
    _label(
      canvas,
      '真实 $total 帧',
      Offset(tail, axisY + 2),
      9,
      const Color(0xFF087E83),
      90,
    );

    // 标记色带。
    for (final marker in markers) {
      final start = (marker['start'] as num?)?.toInt() ?? 0;
      final end = (marker['end'] as num?)?.toInt() ?? start;
      final color = _markerColor('${marker['kind']}');
      final x0 = at(start);
      final x1 = at(end + 1 > start + 1 ? end + 1 : start + 1);
      canvas.drawRect(
        Rect.fromLTRB(x0, axisY, x1 < x0 + 1 ? x0 + 1 : x1, 26),
        Paint()..color = color.withValues(alpha: .16),
      );
    }

    // 片断条。
    var top = 30.0;
    for (final segment in segments) {
      final start = (segment['start'] as num?)?.toInt() ?? 0;
      final end = (segment['end'] as num?)?.toInt() ?? start;
      final isSelected = stageSegmentKey(segment) == selected;
      final conditional = '${segment['condition'] ?? ''}'.isNotEmpty;
      final hasScope = (segment['scope'] as List? ?? []).isNotEmpty;
      final clip = (segment['clip_frames'] as num?)?.toInt() ?? 0;
      final x0 = at(start);
      final x1 = at(end + 1);
      final rect = Rect.fromLTRB(x0, top, x1 < x0 + 3 ? x0 + 3 : x1, top + 15);
      canvas.drawRRect(
        RRect.fromRectAndRadius(rect, const Radius.circular(4)),
        Paint()
          ..color = isSelected
              ? const Color(0xFF087E83)
              : conditional
              ? const Color(0xFFEBC98A)
              : hasScope
              ? const Color(0xFF9CC7CA)
              : const Color(0xFFD8E0E6),
      );
      final text =
          '${segment['name'] ?? ''}'
          '${conditional ? ' · 条件${segment['condition']}' : ''}'
          '${clip == 0 ? '' : ' · $clip帧'}'
          '${hasScope ? ' · 有攻击范围' : ''}';
      _label(
        canvas,
        text,
        Offset(x0 + 5, top + 2),
        10,
        isSelected ? Colors.white : const Color(0xFF26394A),
        x1 - x0 - 10,
      );
      top += 20;
    }
  }

  void _label(
    Canvas canvas,
    String text,
    Offset at,
    double size,
    Color color,
    double maxWidth,
  ) {
    final painter = TextPainter(
      text: TextSpan(
        text: text,
        style: TextStyle(fontSize: size, color: color),
      ),
      textDirection: TextDirection.ltr,
      maxLines: 1,
      ellipsis: '…',
    )..layout(maxWidth: maxWidth < 20 ? 20 : maxWidth);
    painter.paint(canvas, at);
  }

  @override
  bool shouldRepaint(covariant _FrameTrackPainter old) =>
      old.frames != frames ||
      old.selected != selected ||
      old.segments != segments ||
      old.markers != markers;
}

/// 预览里要画的一个盒子（含图例用的颜色）。
class PreviewBox {
  const PreviewBox(this.label, this.attrs, this.color, {this.window = ''});
  final String label;
  final Map<String, String> attrs;
  final Color color;

  /// 力场盒/投技盒按帧生效，这里带上窗口文字（空表示全程）。
  final String window;
}

/// 攻击范围预览：在**带 XYZ 坐标轴**的平面图上叠画
/// 攻击盒（青）、人物盒（灰）、力场盒（橙）、投技盒（紫）。
///
/// 两个视图都是标准正交投影：
/// * **侧视**——屏幕横轴 = **Z 前方**、屏幕纵轴 = **Y 上下**（X 指向屏幕内）；
/// * **顶视**——屏幕横轴 = **X 左右**、屏幕纵轴 = **Z 前方**（Y 指向屏幕外）；
///
/// 原点 (0,0,0) 是角色脚下的位置，所以「地面」就是 y=0 那条线；盒子一律按
/// 中心 ± 尺寸/2 画，因而 y 很低的盒子确实会沉到地面以下——那是数据如此，
/// 画出来就是为了让人一眼看到。
class _ScopePreviewPainter extends CustomPainter {
  _ScopePreviewPainter({
    required this.charBox,
    required this.scope,
    required this.extras,
    required this.side,
  });

  final Map<String, String> charBox;
  final Map<String, String> scope;
  final List<PreviewBox> extras;
  final bool side;

  static const _ink = Color(0xFF26394A);

  double _number(Map<String, String> attrs, String key) =>
      double.tryParse(attrs[key] ?? '') ?? 0;

  /// 屏幕横/纵轴各自对应的世界轴。
  String get _hKey => side ? 'centerz' : 'centerx';
  String get _hSizeKey => side ? 'length' : 'width';
  String get _vKey => side ? 'centery' : 'centerz';
  String get _vSizeKey => side ? 'heigth' : 'length';
  String get _hName => side ? 'Z 前方' : 'X 左右';
  String get _vName => side ? 'Y 上下' : 'Z 前方';

  /// 垂直于屏幕、只能标注的那根轴。
  String get _depthName => side ? 'X 左右（指向屏幕内）' : 'Y 上下（指向屏幕外）';

  /// 纵轴是"地面"的那根轴时，v=0 就是地面。
  String get _groundName => side ? '地面 y=0' : '角色中轴 x=0';

  @override
  void paint(Canvas canvas, Size size) {
    canvas.drawRect(
      Offset.zero & size,
      Paint()..color = const Color(0xFFF7FAFB),
    );

    // 画序：力场盒/投技盒在最底，人物盒居中，正在编辑的攻击盒压在最上面。
    final list = <PreviewBox>[
      ...extras,
      PreviewBox('人物盒', charBox, const Color(0xFF607D8B)),
      PreviewBox('攻击盒', scope, const Color(0xFF087E83)),
    ];

    // ---- 世界范围：原点一定在内，纵轴再保证含 0（地面/中轴） ----
    var hMin = 0.0, hMax = 0.0, vMin = 0.0, vMax = 0.0;
    for (final box in list) {
      final h = _number(box.attrs, _hKey);
      final halfH = _number(box.attrs, _hSizeKey) / 2;
      final v = _number(box.attrs, _vKey);
      final halfV = _number(box.attrs, _vSizeKey) / 2;
      hMin = [hMin, h - halfH].reduce((a, b) => a < b ? a : b);
      hMax = [hMax, h + halfH].reduce((a, b) => a > b ? a : b);
      vMin = [vMin, v - halfV].reduce((a, b) => a < b ? a : b);
      vMax = [vMax, v + halfV].reduce((a, b) => a > b ? a : b);
    }
    // 纵轴两侧各留一点余量，标尺不至于贴边。
    final vPad = [(vMax - vMin) * .12, 4.0].reduce((a, b) => a > b ? a : b);
    vMin -= vPad;
    vMax += vPad;
    final hPad = [(hMax - hMin) * .06, 4.0].reduce((a, b) => a > b ? a : b);
    hMin -= hPad;
    hMax += hPad;

    const left = 46.0, right = 12.0, top = 46.0, bottom = 26.0;
    final hSpan = (hMax - hMin).abs() < 1 ? 1.0 : hMax - hMin;
    final vSpan = (vMax - vMin).abs() < 1 ? 1.0 : vMax - vMin;
    final scale = [
      (size.width - left - right) / hSpan,
      (size.height - top - bottom) / vSpan,
    ].reduce((a, b) => a < b ? a : b);
    // 居中：把世界范围摆在可用矩形中间。
    final freeX = size.width - left - right - hSpan * scale;
    final freeY = size.height - top - bottom - vSpan * scale;
    Offset point(double h, double v) => Offset(
      left + freeX / 2 + (h - hMin) * scale,
      top + freeY / 2 + (vMax - v) * scale,
    );

    // ---- 网格 ----
    final step = _gridStep(hSpan);
    final grid = Paint()
      ..color = const Color(0xFFE6ECF0)
      ..strokeWidth = 1;
    for (var h = (hMin / step).floor() * step; h <= hMax; h += step) {
      canvas.drawLine(point(h, vMin), point(h, vMax), grid);
    }
    for (var v = (vMin / step).floor() * step; v <= vMax; v += step) {
      canvas.drawLine(point(hMin, v), point(hMax, v), grid);
    }

    // ---- 坐标轴 + 刻度 ----
    final axis = Paint()
      ..color = const Color(0xFF90A4AE)
      ..strokeWidth = 1.2;
    final origin = point(0, 0);
    // 纵轴（穿过原点的那条竖线）与横轴。
    canvas.drawLine(
      Offset(origin.dx, top - 30),
      Offset(origin.dx, size.height - bottom + 6),
      axis,
    );
    canvas.drawLine(
      Offset(left - 34, origin.dy),
      Offset(size.width - right + 4, origin.dy),
      axis,
    );
    // 轴线箭头。
    _arrow(canvas, Offset(origin.dx, top - 30), up: true, color: axis.color);
    _arrow(
      canvas,
      Offset(size.width - right + 4, origin.dy),
      up: false,
      color: axis.color,
    );
    _label(
      canvas,
      _vName,
      Offset(origin.dx + 4, top - 42),
      10,
      const Color(0xFF37474F),
      150,
    );
    _label(
      canvas,
      '$_hName →',
      Offset(size.width - right - 76, origin.dy + 3),
      10,
      const Color(0xFF37474F),
      90,
    );
    // 垂直于屏幕的那根轴只能标字（放右上角，避开纵轴标签与刻度）。
    _label(
      canvas,
      _depthName,
      Offset(size.width - right - 240, 6),
      9,
      const Color(0xFF78909C),
      230,
    );

    // 刻度：横轴写在轴线下方，纵轴写在轴线左侧。
    for (var h = (hMin / step).floor() * step; h <= hMax; h += step) {
      if (h.abs() < step / 2) continue;
      final at = point(h, 0);
      _label(
        canvas,
        _trim(h),
        Offset(at.dx - 10, origin.dy + 3),
        8,
        const Color(0xFF90A4AE),
        24,
      );
    }
    for (var v = (vMin / step).floor() * step; v <= vMax; v += step) {
      if (v.abs() < step / 2) continue;
      final at = point(0, v);
      _label(
        canvas,
        _trim(v),
        Offset(origin.dx - 42, at.dy - 5),
        8,
        const Color(0xFF90A4AE),
        30,
      );
    }
    // 地面/中轴加粗标注。
    canvas.drawLine(
      Offset(left - 34, origin.dy),
      Offset(size.width - right + 4, origin.dy),
      Paint()
        ..color = const Color(0xFFB0BEC5)
        ..strokeWidth = 2,
    );
    _label(
      canvas,
      _groundName,
      Offset(origin.dx + 4, origin.dy + 3),
      9,
      const Color(0xFF607D8B),
      120,
    );

    // ---- 盒子（按上面的画序，后画的压在上面） ----
    for (final box in list) {
      final h = _number(box.attrs, _hKey);
      final halfH = _number(box.attrs, _hSizeKey) / 2;
      final v = _number(box.attrs, _vKey);
      final halfV = _number(box.attrs, _vSizeKey) / 2;
      var rect = Rect.fromPoints(
        point(h - halfH, v + halfV),
        point(h + halfH, v - halfV),
      );
      if (rect.right - rect.left < 2) {
        rect = Rect.fromLTRB(
          rect.left - 1,
          rect.top,
          rect.left + 1,
          rect.bottom,
        );
      }
      if (rect.bottom - rect.top < 2) {
        rect = Rect.fromLTRB(rect.left, rect.top - 1, rect.right, rect.top + 1);
      }
      canvas.drawRect(rect, Paint()..color = box.color.withValues(alpha: .13));
      canvas.drawRect(
        rect,
        Paint()
          ..color = box.color
          ..style = PaintingStyle.stroke
          ..strokeWidth = 1.6,
      );
      _label(
        canvas,
        '${box.label}${box.window.isEmpty ? '' : '（${box.window}）'}',
        Offset(rect.left + 3, rect.top + 2),
        10,
        box.color,
        160,
      );
    }
  }

  /// 网格/刻度步长：按 2 的幂伸缩，让画面出现 4–10 条线（也有防死循环的保险）。
  double _gridStep(double span) {
    var step = 1.0;
    var guard = 0;
    while (span / step > 10 && guard++ < 40) {
      step *= 2;
    }
    guard = 0;
    while (span / step < 4 && step > 0.25 && guard++ < 40) {
      step /= 2;
    }
    return step;
  }

  String _trim(double value) {
    final rounded = value.roundToDouble();
    if ((value - rounded).abs() < 0.01) return '${rounded.round()}';
    return value.toStringAsFixed(1);
  }

  void _arrow(
    Canvas canvas,
    Offset tip, {
    required bool up,
    required Color color,
  }) {
    final paint = Paint()
      ..color = color
      ..style = PaintingStyle.fill;
    const size = 4.0;
    final path = Path();
    if (up) {
      path.moveTo(tip.dx, tip.dy - size);
      path.lineTo(tip.dx - size * .7, tip.dy + size * .6);
      path.lineTo(tip.dx + size * .7, tip.dy + size * .6);
    } else {
      path.moveTo(tip.dx + size, tip.dy);
      path.lineTo(tip.dx - size * .6, tip.dy - size * .7);
      path.lineTo(tip.dx - size * .6, tip.dy + size * .7);
    }
    path.close();
    canvas.drawPath(path, paint);
  }

  void _label(
    Canvas canvas,
    String text,
    Offset at,
    double size,
    Color color,
    double maxWidth,
  ) {
    final painter = TextPainter(
      text: TextSpan(
        text: text,
        style: TextStyle(
          fontSize: size,
          color: color,
          fontWeight: FontWeight.w600,
        ),
      ),
      textDirection: TextDirection.ltr,
      maxLines: 1,
      ellipsis: '…',
    )..layout(maxWidth: maxWidth < 20 ? 20 : maxWidth);
    painter.paint(canvas, at);
  }

  @override
  bool shouldRepaint(covariant _ScopePreviewPainter old) =>
      old.side != side ||
      old.charBox != charBox ||
      old.scope != scope ||
      old.extras != extras;
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
                  all ? '包里带上每一把自建武器的素材' : '不勾则只导出「${widget.weaponName}」',
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
                  '默认包含草稿。勾上则只包含本机客户端里'
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
                  title: Text(
                    entry.value,
                    style: const TextStyle(fontSize: 13),
                  ),
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
                  'include': [
                    for (final k in packageSections.keys)
                      if (selected.contains(k)) k,
                  ],
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
  const names = {'7': 'X', '8': 'C', '9': 'Z', '5': '跳', '20': '前', '21': '后'};
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
/// 招架（`<Counter>`）编辑对话框：帧窗口 + 目标状态 + 角度 + 判定盒。
/// `initial` 为空表示这个状态原本没有招架；返回空 Map 表示"删掉这条招架"。
/// 防护 / 自身状态各类型要填的字段。`pick: ustate` 的字段从 ustate.xml 的目录里挑，
/// 其余是整数框（`min`/`max` 与后端校验一致）。
const _blockElementFields = <String, List<Map<String, dynamic>>>{
  'FakeUnAttack': [
    {
      'key': 'startframe',
      'label': '起始帧',
      'min': 0,
      'max': 9999,
      'default': '0',
    },
    {'key': 'endframe', 'label': '结束帧', 'min': 0, 'max': 9999, 'default': '20'},
  ],
  'DirectionalInvc': [
    {
      'key': 'startframe',
      'label': '起始帧',
      'min': 0,
      'max': 9999,
      'default': '0',
    },
    {'key': 'endframe', 'label': '结束帧', 'min': 0, 'max': 9999, 'default': '20'},
    {
      'key': 'angleoffset',
      'label': '朝向角度',
      'min': -180,
      'max': 180,
      'default': '0',
    },
    {
      'key': 'anglehalfrange',
      'label': '半角范围（180=全身）',
      'min': 0,
      'max': 180,
      'default': '55',
    },
  ],
  'BodyGraze': [
    {
      'key': 'startframe',
      'label': '起始帧',
      'min': 0,
      'max': 9999,
      'default': '0',
    },
    {'key': 'endframe', 'label': '结束帧', 'min': 0, 'max': 9999, 'default': '20'},
  ],
  'UState': [
    {'key': 'id', 'label': '状态', 'pick': 'ustate'},
    {'key': 'level', 'label': '等级', 'min': -999, 'max': 999, 'default': '1'},
    {
      'key': 'duration',
      'label': '时长(ms)',
      'min': 0,
      'max': 9999999,
      'default': '1500',
    },
  ],
  'Ustate': [
    {'key': 'id', 'label': '状态', 'pick': 'ustate'},
  ],
  'AddBuff': [
    {'key': 'frame', 'label': '触发帧', 'min': 0, 'max': 9999, 'default': '0'},
    {'key': 'UnNormalState', 'label': '状态', 'pick': 'ustate'},
    {
      'key': 'UStateLevel',
      'label': '等级',
      'min': -999,
      'max': 999,
      'default': '1',
    },
    {
      'key': 'UStateLastCycle',
      'label': '时长(ms)',
      'min': 0,
      'max': 9999999,
      'default': '1500',
    },
    {
      'key': 'Scope',
      'label': '作用对象',
      'pick': 'scope',
      'min': 0,
      'max': 255,
      'default': '1',
    },
    {'key': 'Param1', 'label': '范围（作用对象=3/5 时有效）', 'default': ''},
  ],
  'SelfControl': [
    {
      'key': 'startframe',
      'label': '起始帧',
      'min': 0,
      'max': 9999,
      'default': '0',
    },
    {'key': 'endframe', 'label': '结束帧', 'min': 0, 'max': 9999, 'default': '30'},
    {
      'key': 'turnspeed',
      'label': '转向速度',
      'min': 0,
      'max': 9999,
      'default': '90',
    },
    {
      'key': 'movespeed',
      'label': '移动速度',
      'min': 0,
      'max': 9999,
      'default': '1',
    },
    {'key': 'turnadd', 'label': '附加转向', 'min': 0, 'max': 999, 'default': '0'},
  ],
  'HideBody': [
    {
      'key': 'startframe',
      'label': '起始帧',
      'min': 0,
      'max': 9999,
      'default': '3',
    },
    {'key': 'endframe', 'label': '结束帧', 'min': 0, 'max': 9999, 'default': '13'},
    {'key': 'type', 'label': '类型', 'min': 0, 'max': 9, 'default': '1'},
  ],
  'ForceField': [
    {
      'key': 'startframe',
      'label': '起始帧',
      'min': 0,
      'max': 9999,
      'default': '0',
    },
    {
      'key': 'endframe',
      'label': '结束帧',
      'min': 0,
      'max': 9999,
      'default': '999',
    },
    {'key': 'pushspeed', 'label': '推开速度', 'min': 0, 'max': 999, 'default': '5'},
    {
      'key': 'friendlyfire',
      'label': '误伤队友',
      'min': 0,
      'max': 1,
      'default': '1',
    },
  ],
};

/// 力场（ForceField）的子元素 <ScopeBox> 要填的字段。
const _blockElementChildFields = <String, List<Map<String, dynamic>>>{
  'ForceField': [
    {
      'key': 'centerx',
      'label': '盒心 x',
      'min': -9999,
      'max': 9999,
      'default': '0',
    },
    {
      'key': 'centery',
      'label': '盒心 y',
      'min': -9999,
      'max': 9999,
      'default': '70',
    },
    {
      'key': 'centerz',
      'label': '盒心 z',
      'min': -9999,
      'max': 9999,
      'default': '10',
    },
    {'key': 'length', 'label': '长', 'min': -9999, 'max': 9999, 'default': '80'},
    {'key': 'width', 'label': '宽', 'min': -9999, 'max': 9999, 'default': '80'},
    {
      'key': 'heigth',
      'label': '高',
      'min': -9999,
      'max': 9999,
      'default': '160',
    },
  ],
};

/// `<AddBuff Scope>` 的「作用对象」档位。定义出自 `animation/2001.xml` 文件头的
/// 策划使用说明注释（全库只有那里写了）：
/// `Scope="1"对自己使用，"2"对自己包括队友使用"3"对自己包括队友使用 Param1值是范围，
/// "4"对敌人不包括自己，"5"对敌人不包括自己 Param1值是范围，"6"对死亡的队友中等级最高的人使用。`
/// 注意：**Scope 是作用对象，Param1 才是范围**（只有 3 和 5 用到）。
const scopeChoices = <Map<String, String>>[
  {'v': '1', 'l': '1 · 对自己使用'},
  {'v': '2', 'l': '2 · 对自己包括队友使用'},
  {'v': '3', 'l': '3 · 对自己包括队友使用（Param1=范围）'},
  {'v': '4', 'l': '4 · 对敌人不包括自己'},
  {'v': '5', 'l': '5 · 对敌人不包括自己（Param1=范围）'},
  {'v': '6', 'l': '6 · 对死亡的队友中等级最高的人使用'},
];

/// 只有这两个档位的 Param1 是"范围"。
bool scopeUsesParam(String scope) => scope == '3' || scope == '5';

bool _scopeKnown(String value) =>
    scopeChoices.any((option) => option['v'] == value);

/// 添加/编辑一条防护或自身状态元素。条目形状 = {tag, attrs:[{key,value}]}。
class _BlockElementDialog extends StatefulWidget {
  const _BlockElementDialog({
    required this.tag,
    required this.label,
    required this.initial,
    required this.ustates,
  });

  final String tag;
  final String label;
  final Map<String, dynamic>? initial;
  final List<Map<String, String>> ustates;

  @override
  State<_BlockElementDialog> createState() => _BlockElementDialogState();
}

class _BlockElementDialogState extends State<_BlockElementDialog> {
  final form = GlobalKey<FormState>();
  final values = <String, String>{};

  @override
  void initState() {
    super.initState();
    final attrs = <String, String>{
      for (final a in (widget.initial?['attrs'] as List? ?? []))
        '${(a as Map)['key']}': '${a['value']}',
    };
    final firstUstate = widget.ustates.isNotEmpty
        ? widget.ustates.first['id']
        : '';
    for (final field in _blockElementFields[widget.tag] ?? const []) {
      final key = '${field['key']}';
      final fallback = field['pick'] == 'ustate'
          ? '${firstUstate ?? ''}'
          : '${field['default'] ?? '0'}';
      values[key] = attrs.containsKey(key) ? attrs[key]! : fallback;
    }
    final boxAttrs = <String, String>{
      for (final a in (widget.initial?['box'] as List? ?? []))
        '${(a as Map)['key']}': '${a['value']}',
    };
    for (final field in _blockElementChildFields[widget.tag] ?? const []) {
      final key = '${field['key']}';
      values[key] = boxAttrs.containsKey(key)
          ? boxAttrs[key]!
          : '${field['default'] ?? '0'}';
    }
  }

  String? number(String? value, String label, int min, int max) {
    final text = (value ?? '').trim();
    if (text.isEmpty) return '请填 $label';
    final parsed = int.tryParse(text);
    if (parsed == null || parsed < min || parsed > max) {
      return '$label 需要 $min..$max 的整数';
    }
    return null;
  }

  void submit() {
    if (!(form.currentState?.validate() ?? false)) return;
    final attrs = <Map<String, String>>[];
    for (final field in _blockElementFields[widget.tag] ?? const []) {
      final key = '${field['key']}';
      attrs.add({'key': key, 'value': (values[key] ?? '').trim()});
    }
    final box = <Map<String, String>>[];
    for (final field in _blockElementChildFields[widget.tag] ?? const []) {
      final key = '${field['key']}';
      box.add({'key': key, 'value': (values[key] ?? '').trim()});
    }
    Navigator.pop(context, {
      'tag': widget.tag,
      'attrs': attrs,
      if (box.isNotEmpty) 'box': box,
    });
  }

  @override
  Widget build(BuildContext context) {
    final fields = _blockElementFields[widget.tag] ?? const [];
    return AlertDialog(
      title: Text('${widget.label}（${widget.tag}）'),
      content: SizedBox(
        width: 420,
        child: Form(
          key: form,
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (fields.isEmpty)
                  Text('这个元素没有可编辑的字段：${widget.tag}')
                else
                  for (final field in fields) ...[
                    if (field['pick'] == 'ustate')
                      DropdownButtonFormField<String>(
                        value:
                            widget.ustates.any(
                              (u) => u['id'] == values['${field['key']}'],
                            )
                            ? values['${field['key']}']
                            : null,
                        decoration: InputDecoration(
                          labelText: '${field['label']}',
                          isDense: true,
                        ),
                        items: [
                          for (final u in widget.ustates)
                            DropdownMenuItem(
                              value: u['id'],
                              child: Text('${u['id']}（${u['name']}）'),
                            ),
                        ],
                        onChanged: (v) =>
                            setState(() => values['${field['key']}'] = v ?? ''),
                        validator: (v) => (v ?? '').isEmpty ? '请选择状态' : null,
                      )
                    else if (field['pick'] == 'scope')
                      DropdownButtonFormField<String>(
                        key: ValueKey('scope-${values['${field['key']}']}'),
                        value: (values['${field['key']}'] ?? '').isEmpty
                            ? null
                            : values['${field['key']}'],
                        decoration: const InputDecoration(
                          labelText: '作用对象（谁吃到这个状态）',
                          isDense: true,
                          helperMaxLines: 2,
                          helperText:
                              '1 自己 / 2 自己+队友 / 3 自己+队友+范围 / '
                              '4 敌人 / 5 敌人+范围 / 6 阵亡队友中等级最高者',
                        ),
                        items: [
                          for (final option in scopeChoices)
                            DropdownMenuItem(
                              value: option['v'],
                              child: Text('${option['l']}'),
                            ),
                          // 数据里出现过未收录的值（如 40），原样保留而不是偷偷改掉。
                          if (!_scopeKnown(values['${field['key']}'] ?? ''))
                            DropdownMenuItem(
                              value: values['${field['key']}'],
                              child: Text(
                                '${values['${field['key']}']} · 未收录（原样保留）',
                              ),
                            ),
                        ],
                        onChanged: (v) => setState(() {
                          // 只换作用对象，不动 Param1 —— 免得静默改掉原值
                          // （数据里确实存在 Scope=1 + Param1="0" 的写法）。
                          values['${field['key']}'] = v ?? '1';
                        }),
                        validator: (v) => (v ?? '').isEmpty ? '请选择作用对象' : null,
                      )
                    else
                      TextFormField(
                        initialValue: values['${field['key']}'],
                        enabled:
                            field['key'] != 'Param1' ||
                            scopeUsesParam(values['Scope'] ?? ''),
                        decoration: InputDecoration(
                          labelText: '${field['label']}',
                          isDense: true,
                        ),
                        onChanged: (v) => values['${field['key']}'] = v,
                        validator: field['min'] == null
                            ? null
                            : (v) => number(
                                v,
                                '${field['label']}',
                                field['min'] as int,
                                field['max'] as int,
                              ),
                      ),
                    const SizedBox(height: 8),
                  ],
                if ((_blockElementChildFields[widget.tag] ?? const [])
                    .isNotEmpty) ...[
                  const SizedBox(height: 4),
                  const Text(
                    '作用盒（子元素）',
                    style: TextStyle(fontSize: 12, fontWeight: FontWeight.w600),
                  ),
                  const SizedBox(height: 4),
                  for (final field
                      in _blockElementChildFields[widget.tag] ?? const []) ...[
                    TextFormField(
                      initialValue: values['${field['key']}'],
                      decoration: InputDecoration(
                        labelText: '${field['label']}',
                        isDense: true,
                      ),
                      onChanged: (v) => values['${field['key']}'] = v,
                      validator: (v) => number(
                        v,
                        '${field['label']}',
                        field['min'] as int,
                        field['max'] as int,
                      ),
                    ),
                    const SizedBox(height: 8),
                  ],
                ],
                const Text(
                  '保存后还要点「应用到游戏」才会写进配置包；共用动作块会先克隆成该武器独占的块。',
                  style: TextStyle(fontSize: 11, color: Colors.black54),
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
        FilledButton(onPressed: submit, child: const Text('确定')),
      ],
    );
  }
}

class _CounterDialog extends StatefulWidget {
  const _CounterDialog({
    required this.state,
    required this.states,
    required this.initial,
  });

  final String state;
  final List<String> states;
  final Map<String, dynamic>? initial;

  @override
  State<_CounterDialog> createState() => _CounterDialogState();
}

class _CounterDialogState extends State<_CounterDialog> {
  final form = GlobalKey<FormState>();
  late final TextEditingController start;
  late final TextEditingController end;
  late final TextEditingController offset;
  late final TextEditingController half;
  late final TextEditingController centerX;
  late final TextEditingController centerY;
  late final TextEditingController centerZ;
  late final TextEditingController length;
  late final TextEditingController width;
  late final TextEditingController heigth;
  late String next;

  @override
  void initState() {
    super.initState();
    final attrs = <String, String>{
      for (final a in (widget.initial?['attrs'] as List? ?? []))
        '${(a as Map)['key']}': '${a['value']}',
    };
    final box = <String, String>{
      for (final a in (widget.initial?['box'] as List? ?? []))
        '${(a as Map)['key']}': '${a['value']}',
    };
    start = TextEditingController(text: attrs['startframe'] ?? '0');
    end = TextEditingController(text: attrs['endframe'] ?? '25');
    offset = TextEditingController(text: attrs['angleoffset'] ?? '0');
    half = TextEditingController(text: attrs['anglehalfrange'] ?? '55');
    centerX = TextEditingController(text: box['centerx'] ?? '0');
    centerY = TextEditingController(text: box['centery'] ?? '15');
    centerZ = TextEditingController(text: box['centerz'] ?? '6');
    length = TextEditingController(text: box['length'] ?? '22');
    width = TextEditingController(text: box['width'] ?? '20');
    heigth = TextEditingController(text: box['heigth'] ?? '20');
    final wanted = attrs['nextstate'] ?? widget.state;
    next = widget.states.contains(wanted)
        ? wanted
        : (widget.states.isNotEmpty ? widget.states.first : '');
  }

  @override
  void dispose() {
    for (final c in [
      start,
      end,
      offset,
      half,
      centerX,
      centerY,
      centerZ,
      length,
      width,
      heigth,
    ]) {
      c.dispose();
    }
    super.dispose();
  }

  String? frameField(String? value, String label, int min, int max) {
    final text = (value ?? '').trim();
    final number = int.tryParse(text);
    if (number == null || number < min || number > max) {
      return '$label 需要 $min..$max 的整数';
    }
    return null;
  }

  void submit() {
    if (!(form.currentState?.validate() ?? false)) return;
    // 只写客户端认识的属性，顺序固定：start/end/nextstate/角度。
    final attrs = <Map<String, String>>[
      {'key': 'startframe', 'value': start.text.trim()},
      {'key': 'endframe', 'value': end.text.trim()},
      {'key': 'nextstate', 'value': next},
      {'key': 'angleoffset', 'value': offset.text.trim()},
      {'key': 'anglehalfrange', 'value': half.text.trim()},
    ];
    final box = <Map<String, String>>[
      {'key': 'centerx', 'value': centerX.text.trim()},
      {'key': 'centery', 'value': centerY.text.trim()},
      {'key': 'centerz', 'value': centerZ.text.trim()},
      {'key': 'length', 'value': length.text.trim()},
      {'key': 'width', 'value': width.text.trim()},
      {'key': 'heigth', 'value': heigth.text.trim()},
    ];
    Navigator.pop(context, {'attrs': attrs, 'box': box});
  }

  Widget numberField(
    TextEditingController controller,
    String label,
    int min,
    int max, {
    double width = 108,
  }) {
    return SizedBox(
      width: width,
      child: TextFormField(
        controller: controller,
        decoration: InputDecoration(labelText: label, isDense: true),
        validator: (v) => frameField(v, label, min, max),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text('状态 ${widget.state} 的招架'),
      content: SizedBox(
        width: 460,
        child: Form(
          key: form,
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text(
                  '招架是「被对手攻击触发」的转移：这些帧内对手的攻击打进来，'
                  '且来向在角度范围内，就切到目标状态。不是按键触发的连招。',
                  style: TextStyle(fontSize: 12),
                ),
                const SizedBox(height: 10),
                Row(
                  children: [
                    numberField(start, '起始帧', 0, 9999),
                    const SizedBox(width: 8),
                    numberField(end, '结束帧', 0, 9999),
                  ],
                ),
                const SizedBox(height: 8),
                DropdownButtonFormField<String>(
                  value: next.isEmpty ? null : next,
                  decoration: const InputDecoration(
                    labelText: '目标状态',
                    isDense: true,
                  ),
                  items: [
                    for (final s in widget.states)
                      DropdownMenuItem(value: s, child: Text(s)),
                  ],
                  onChanged: (v) => setState(() => next = v ?? ''),
                ),
                const SizedBox(height: 8),
                Row(
                  children: [
                    numberField(offset, '朝向角度', -180, 180),
                    const SizedBox(width: 8),
                    numberField(half, '半角范围', 0, 180),
                  ],
                ),
                const SizedBox(height: 10),
                const Text('判定盒（相对角色原点）', style: TextStyle(fontSize: 12)),
                const SizedBox(height: 4),
                Row(
                  children: [
                    numberField(centerX, 'x', -9999, 9999, width: 74),
                    const SizedBox(width: 6),
                    numberField(centerY, 'y', -9999, 9999, width: 74),
                    const SizedBox(width: 6),
                    numberField(centerZ, 'z', -9999, 9999, width: 74),
                  ],
                ),
                const SizedBox(height: 6),
                Row(
                  children: [
                    numberField(length, '长', -9999, 9999, width: 74),
                    const SizedBox(width: 6),
                    numberField(width, '宽', -9999, 9999, width: 74),
                    const SizedBox(width: 6),
                    numberField(heigth, '高', -9999, 9999, width: 74),
                  ],
                ),
                const SizedBox(height: 8),
                const Text(
                  '删除这条招架：返回列表后点状态右侧的 ×，或整把武器「清除招架定制」。',
                  style: TextStyle(fontSize: 11, color: Colors.black54),
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
        FilledButton(onPressed: submit, child: const Text('确定')),
      ],
    );
  }
}

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
    // 空 keycode = 「自动」：不写 keycode 属性，也不该带输入窗口（输入窗口是用来
    // 判定按键时机的，没有按键就没有窗口）。旧数据里两者可能并存，这里以「有键
    // 才写窗口」为准，避免产出一个既不按键又要求按键时机的矛盾条目。
    final hasKey = resolved != null && resolved.isNotEmpty;
    if (hasKey) codes['keycode'] = resolved;
    final startIn = inputStart.text.trim();
    final endIn = inputEnd.text.trim();
    if (hasKey && startIn.isNotEmpty && endIn.isNotEmpty) {
      codes['inputstartframe'] = startIn;
      codes['inputendframe'] = endIn;
    }
    final gap = interval.text.trim();
    if (hasKey && gap.isNotEmpty) codes['keyintervalframe'] = gap;
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
                  initialValue: customKey
                      ? '__custom__'
                      : (key.isEmpty ? '__auto__' : key),
                  decoration: const InputDecoration(labelText: '按键'),
                  items: [
                    const DropdownMenuItem(
                      value: '__auto__',
                      child: Text('（自动）不按键，播到生效窗口直接切换'),
                    ),
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
                    if (value == '__auto__') {
                      key = '';
                    } else if (!customKey && value != null) {
                      key = value;
                    }
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
/// 判断一个命中属性号是否属于服务端分配给动作分支的范围。
bool isVariantSkillProID(
  String value, {
  int minimum = 910000000,
  int maximum = 910999999,
}) {
  final number = int.tryParse(value.trim());
  return number != null && number >= minimum && number <= maximum;
}

/// 在服务端给定范围内返回第一个未占用的动作分支命中属性号。
String? nextVariantSkillProID(
  Set<String> occupied, {
  int minimum = 910000000,
  int maximum = 910999999,
}) {
  for (var value = minimum; value <= maximum; value++) {
    if (!occupied.contains('$value')) return '$value';
  }
  return null;
}

/// 「动作分支」里的一段动作（动画名 + 帧区间）。
///
/// skillproid / replayTimes / anmId 不在这里编辑，但要**原样带回**：它们是
/// 「这一段还是原来那段」的凭据，丢了就等于把整段重建一遍（换号、丢卡帧）。
/// skillproid 可变：用户可以在分支里给这一段增加命中属性，选完立刻显示 id。
bool _isVariantSkillProID(String value) => isVariantSkillProID(value);

class _VariantRow {
  _VariantRow({
    String name = '',
    String start = '0',
    String end = '0',
    this.segmentDamage = '',
    this.skillproid = '',
    this.templateSkillproid = '',
    this.replayTimes = 0,
    this.anmId = '',
  }) : nameCtrl = TextEditingController(text: name),
       startCtrl = TextEditingController(text: start),
       endCtrl = TextEditingController(text: end),
       replayCtrl = TextEditingController(text: '$replayTimes');

  final TextEditingController nameCtrl;
  final TextEditingController startCtrl;
  final TextEditingController endCtrl;
  final TextEditingController replayCtrl;
  String segmentDamage;

  /// 最终写入动作段的 ID。新段在点击“增加命中属性”时立即预分配。
  String skillproid;

  /// 创建该最终 ID 时复制的模板属性 ID；旧数据没有该字段时兼容回退。
  String templateSkillproid;
  int replayTimes;
  final String anmId;

  /// 这一段打不打人（挂没挂命中属性）。纯动作段 / 卡帧段不挂。
  bool get hasHit => skillproid.trim().isNotEmpty;

  void dispose() {
    nameCtrl.dispose();
    startCtrl.dispose();
    endCtrl.dispose();
    replayCtrl.dispose();
  }
}

/// 动作分支对话框要用的联动上下文（只读的三件套）。
class _VariantHitContext {
  const _VariantHitContext(
    this.hits,
    this.overrides,
    this.applied,
    this.occupied,
    this.localReserved,
    this.minimum,
    this.maximum,
  );

  /// 本状态（含分支）的命中属性明细：id → {values, buff, variant}。
  final Map<String, Map<String, dynamic>> hits;

  /// 该状态当前的命中属性数值覆盖（rule['properties']）。
  final Map<String, dynamic> overrides;

  /// 已经写进配置包的命中属性号；不在里面的要先「应用到游戏」才能改数值。
  final Set<String> applied;
  final Set<String> occupied;
  final Set<String> localReserved;
  final int minimum;
  final int maximum;
}

/// 编辑某状态的一条动作分支：选触发状态 + 定义动作段。
/// 段列表留空 = 交给后端照抄无条件块，只换命中编号（skillproid）。
///
/// 增加命中属性时预占最终编号并直接编辑；数值保存在 rule['properties'][id]，
/// 与连招命中编辑器共享。取消对话框不会改动父级草稿。
class _VariantBranchDialog extends StatefulWidget {
  const _VariantBranchDialog({
    required this.ustates,
    required this.stateLabel,
    required this.initial,
    required this.baseSegments,
    required this.api,
    required this.hits,
    required this.hitOverrides,
    required this.fields,
    required this.effects,
    required this.hitOptions,
    required this.allowed,
    required this.applied,
    required this.occupied,
    required this.localReserved,
    required this.minimum,
    required this.maximum,
  });

  final List<Map<String, String>> ustates;
  final String stateLabel;
  final Map<String, dynamic>? initial;
  final List<Map<String, dynamic>> baseSegments;

  /// 客户端里现成的命中属性目录按需拉取（weapon_remap_options），用来给某一段挑模板。
  final Future<dynamic> Function(Map<String, dynamic>) api;

  /// 本状态（含分支）的命中属性明细：id → {values, buff, variant}。
  final Map<String, Map<String, dynamic>> hits;

  /// 本状态当前的命中属性数值覆盖（rule['properties']）。
  final Map<String, dynamic> hitOverrides;
  final List fields;
  final List effects;
  final Map<String, dynamic> hitOptions;
  final Map<String, dynamic> allowed;

  /// 已经写进配置包的命中属性号；仅用于标注待应用状态，不限制草稿编辑。
  final Set<String> applied;
  final Set<String> occupied;
  final Set<String> localReserved;
  final int minimum;
  final int maximum;

  @override
  State<_VariantBranchDialog> createState() => _VariantBranchDialogState();
}

class _VariantBranchDialogState extends State<_VariantBranchDialog> {
  String condition = '';
  final rows = <_VariantRow>[];
  final dropped = <_VariantRow>[];
  final overrides = <String, Map<String, dynamic>>{};
  final reservedIDs = <String>{};
  final sessionReservedIDs = <String>{};
  late int nextReservedID;
  String error = '';

  @override
  void initState() {
    super.initState();
    reservedIDs.addAll(widget.occupied);
    reservedIDs.addAll(widget.hits.keys.where(_isVariantSkillProID));
    reservedIDs.addAll(widget.applied.where(_isVariantSkillProID));
    nextReservedID = widget.minimum;
    final initial = widget.initial;
    if (initial != null) {
      condition = '${initial['condition'] ?? ''}';
      final initialSegments = initial['segments'] as List? ?? const [];
      for (final s in initialSegments) {
        final map = Map<String, dynamic>.from(s as Map);
        final rawID = '${map['skillproid'] ?? ''}'.trim();
        final legacy =
            initial['origin'] == 'saved' &&
            '${map['template_skillproid'] ?? ''}'.trim().isEmpty &&
            rawID.isNotEmpty &&
            !_isVariantSkillProID(rawID);
        if (!legacy && rawID.isNotEmpty) reservedIDs.add(rawID);
      }
      for (final s in initialSegments) {
        final map = Map<String, dynamic>.from(s as Map);
        var finalID = '${map['skillproid'] ?? ''}'.trim();
        var templateID = '${map['template_skillproid'] ?? ''}'.trim();
        // 仅后端明确标记为已保存的旧作者态才把旧字段解释为模板号。
        if (initial['origin'] == 'saved' &&
            templateID.isEmpty &&
            finalID.isNotEmpty &&
            !_isVariantSkillProID(finalID)) {
          templateID = finalID;
          final allocated = reserveID();
          if (allocated.isNotEmpty) finalID = allocated;
          if (allocated.isNotEmpty) {
            overrides[finalID] = _extractHitValues(
              widget.hitOverrides[templateID],
            );
          }
        }
        if (finalID.isNotEmpty) {
          reservedIDs.add(finalID);
          if (widget.localReserved.contains(finalID)) {
            sessionReservedIDs.add(finalID);
          }
        }
        rows.add(
          _VariantRow(
            name: '${map['name'] ?? ''}',
            start: '${map['start'] ?? 0}',
            end: '${map['end'] ?? 0}',
            segmentDamage: map['damage'] == null ? '' : '${map['damage']}',
            skillproid: finalID,
            templateSkillproid: templateID,
            replayTimes: int.tryParse('${map['replay_times'] ?? 0}') ?? 0,
            anmId: '${map['anm_id'] ?? ''}',
          ),
        );
      }
    }
    // 深拷一份，取消时不影响外部；段里用到的号才带进来。
    widget.hitOverrides.forEach((id, value) {
      final values = _extractHitValues(value);
      if (values.isNotEmpty) overrides['$id'] = values;
    });
    for (final row in rows) {
      final damage = num.tryParse(row.segmentDamage);
      if (row.hasHit &&
          !widget.hits.containsKey(row.skillproid) &&
          damage != null) {
        overrides
            .putIfAbsent(row.skillproid, () => <String, dynamic>{})
            .putIfAbsent('SkillDamage', () => damage);
      }
    }
  }

  @override
  void dispose() {
    for (final r in [...rows, ...dropped]) {
      r.dispose();
    }
    super.dispose();
  }

  void copyBase() {
    setState(() {
      final hitTemplates = widget.baseSegments
          .where(
            (segment) => '${segment['skillproid'] ?? ''}'.trim().isNotEmpty,
          )
          .length;
      final reusable = {...reservedIDs};
      for (final row in rows) {
        final id = row.skillproid.trim();
        if (sessionReservedIDs.contains(id)) reusable.remove(id);
      }
      final available =
          widget.maximum -
          widget.minimum +
          1 -
          reusable.where((id) {
            final value = int.tryParse(id);
            return value != null &&
                value >= widget.minimum &&
                value <= widget.maximum;
          }).length;
      if (hitTemplates > available) {
        error = '变体命中属性编号不足，无法照抄全部命中段';
        return;
      }
      for (final row in rows) {
        releaseID(row.skillproid.trim());
        overrides.remove(row.skillproid.trim());
      }
      dropped.addAll(rows);
      rows.clear();
      for (final s in widget.baseSegments) {
        final source = '${s['skillproid'] ?? ''}'.trim();
        final id = source.isEmpty ? '' : reserveID();
        if (id.isNotEmpty) {
          final damage = num.tryParse('${s['damage'] ?? ''}');
          overrides[id] = _mergeHitValueMaps([
            widget.hits[source],
            widget.hitOverrides[source],
            if (damage != null) {'SkillDamage': damage},
          ]);
        }
        rows.add(
          _VariantRow(
            name: '${s['name'] ?? ''}',
            start: '${s['start'] ?? 0}',
            end: '${s['end'] ?? 0}',
            // 无条件块的命中编号作为模板源；命中段同步预分配最终 ID，纯动作段保持无号。
            segmentDamage: s['damage'] == null ? '' : '${s['damage']}',
            skillproid: id,
            templateSkillproid: '${s['skillproid'] ?? ''}',
            replayTimes: int.tryParse('${s['replay_times'] ?? 0}') ?? 0,
            anmId: '${s['anm_id'] ?? ''}',
          ),
        );
      }
      error = '';
    });
  }

  String reserveID() {
    final id = nextVariantSkillProID(
      reservedIDs,
      minimum: nextReservedID,
      maximum: widget.maximum,
    );
    if (id == null) {
      error = '变体命中属性编号已用完（${widget.minimum}–${widget.maximum}）';
      return '';
    }
    final value = int.parse(id);
    nextReservedID = value + 1;
    reservedIDs.add(id);
    sessionReservedIDs.add(id);
    return id;
  }

  void releaseID(String id) {
    if (sessionReservedIDs.remove(id)) {
      reservedIDs.remove(id);
      final value = int.tryParse(id);
      if (value != null && value < nextReservedID) nextReservedID = value;
    }
  }

  bool hasTemplateValues(String id) =>
      id.isNotEmpty && widget.hits[id]?['values'] is Map;

  String automaticTemplateFor(int index) {
    final current = rows[index].templateSkillproid.trim();
    if (hasTemplateValues(current)) return current;
    // hits follows the state's actual hit order; new hits inherit its first hit.
    for (final entry in widget.hits.entries) {
      if ('${entry.value['variant'] ?? ''}'.isEmpty &&
          hasTemplateValues(entry.key)) return entry.key;
    }
    for (final segment in widget.baseSegments) {
      final source = '${segment['skillproid'] ?? ''}'.trim();
      if (hasTemplateValues(source)) return source;
    }
    return '';
  }

  /// 点击后立即预占最终分支 ID并打开共用的命中属性编辑器，不再先让用户挑模板。
  Future<void> addHit(int index) async {
    final template = automaticTemplateFor(index);
    if (template.isEmpty) {
      setState(() => error = '当前动作没有带实际数值的命中属性模板，暂不能增加命中属性');
      return;
    }
    final row = rows[index];
    final oldID = row.skillproid.trim();
    releaseID(oldID);
    overrides.remove(oldID);
    final id = reserveID();
    if (id.isEmpty) {
      setState(() {});
      return;
    }
    setState(() {
      row.templateSkillproid = template;
      row.skillproid = id;
      row.replayTimes = 0;
      row.replayCtrl.text = '0';
      error = '';
    });
    await editHit(index);
  }

  /// 改某一段命中属性的数值。预占但尚未应用的 ID也允许编辑。
  Future<void> editHit(int index) async {
    final row = rows[index];
    final id = row.skillproid.trim();
    if (id.isEmpty) return;
    final source = row.templateSkillproid.trim();
    final hit =
        widget.hits[id] ?? (source.isEmpty ? null : widget.hits[source]);
    final initial = _mergeHitValueMaps([
      hit,
      widget.hitOverrides[source],
      overrides[id],
    ]);
    if (!widget.hits.containsKey(id) &&
        row.segmentDamage.isNotEmpty &&
        !overrides.containsKey(id)) {
      final damage = num.tryParse(row.segmentDamage);
      if (damage != null) initial.putIfAbsent('SkillDamage', () => damage);
    }
    final originalValues = _mergeHitValueMaps([hit, widget.hitOverrides[source]]);
    final original = <String, dynamic>{
      for (final field in widget.fields)
        '${field['key']}': '${originalValues[field['key']] ?? 0}',
    };
    final result = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _HitPropertyDialog(
        hitId: id,
        variant: condition,
        original: original,
        initial: initial,
        fields: widget.fields,
        effects: widget.effects,
        hitOptions: widget.hitOptions,
        allowed: widget.allowed,
      ),
    );
    if (result == null || !mounted) return;
    setState(() {
      overrides[id] = _mergeHitValueMaps([initial, result]);
      row.segmentDamage = '${overrides[id]?['SkillDamage'] ?? ''}';
      error = '';
    });
  }

  dynamic _normalizeVariantDamage(dynamic value) {
    if (value is num) return value;
    return num.tryParse('$value'.trim()) ?? value;
  }

  void submit() {
    if (rows.any(
      (row) =>
          row.skillproid.isNotEmpty &&
          row.skillproid == row.templateSkillproid &&
          !_isVariantSkillProID(row.skillproid),
    )) {
      setState(() => error = '变体命中属性编号不足，无法转换旧分支；请清除命中属性或取消');
      return;
    }
    final cond = int.tryParse(condition.trim());
    if (cond == null || cond <= 0) {
      setState(() => error = '请选择触发状态（必须是 ustate 编号）');
      return;
    }
    final segments = <Map<String, dynamic>>[];
    for (var i = 0; i < rows.length; i++) {
      final r = rows[i];
      final name = r.nameCtrl.text.trim();
      if (name.isEmpty) {
        setState(() => error = '第 ${i + 1} 段缺少动画名');
        return;
      }
      final start = int.tryParse(r.startCtrl.text.trim());
      final end = int.tryParse(r.endCtrl.text.trim());
      final replay = int.tryParse(r.replayCtrl.text.trim());
      if (start == null || end == null || start < 0 || end < start) {
        setState(() => error = '第 ${i + 1} 段的帧区间不合法（需从 0 开始且结束帧 ≥ 开始帧）');
        return;
      }
      if (replay == null || replay < 0) {
        setState(() => error = '第 ${i + 1} 段的卡帧必须是大于等于 0 的整数');
        return;
      }
      segments.add({
        'name': name,
        'start': start,
        'end': end,
        // 原样回带：不走这两行的话，打开分支再点「确定」就会把这一段重建成新号
        // （旧号变孤儿、卡帧丢失），也就是「同一份配置每次保存都不一样」。
        if (r.skillproid.trim().isNotEmpty) 'skillproid': r.skillproid.trim(),
        if (r.templateSkillproid.trim().isNotEmpty)
          'template_skillproid': r.templateSkillproid.trim(),
        if (overrides[r.skillproid]?['SkillDamage'] != null ||
            r.segmentDamage.isNotEmpty)
          'damage': _normalizeVariantDamage(
            overrides[r.skillproid]?['SkillDamage'] ?? r.segmentDamage,
          ),
        if (r.skillproid.trim().isNotEmpty || replay > 0)
          'replay_times': replay,
        if (r.anmId.isNotEmpty) 'anm_id': r.anmId,
      });
    }
    // 只回带这一段仍挂着的号：段被删掉时，它的命中属性数值也一起撤掉
    // （真删属性节点靠后端应用时清孤儿，这里先保证规则不再引用它）。
    final hitProperties = <String, dynamic>{};
    for (final s in segments) {
      final id = '${s['skillproid'] ?? ''}'.trim();
      if (id.isEmpty) continue;
      final source = '${s['template_skillproid'] ?? ''}'.trim();
      hitProperties[id] = _mergeHitValueMaps([
        // 这是完整快照，不是删除式 patch：新分支换掉原段时，模板的
        // SkillDamage 和高级字段仍要随新 ID 一起提交。
        widget.hits[source],
        widget.hitOverrides[source],
        widget.hits[id],
        overrides[id],
      ]);
    }
    Navigator.pop(context, {
      'condition': cond,
      'segments': segments,
      'hit_properties': hitProperties,
      'local_ids': sessionReservedIDs.toList(),
    });
  }

  @override
  Widget build(BuildContext context) {
    final known = widget.ustates.any((u) => u['id'] == condition);
    return AlertDialog(
      title: Text('动作分支 · ${widget.stateLabel}'),
      content: SizedBox(
        width: 620,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DropdownButtonFormField<String>(
                value: known ? condition : null,
                decoration: const InputDecoration(
                  labelText: '触发条件（持有该状态时换成这套动作）',
                  isDense: true,
                ),
                items: [
                  for (final u in widget.ustates)
                    DropdownMenuItem(
                      value: u['id'],
                      child: Text('${u['id']}（${u['name']}）'),
                    ),
                ],
                onChanged: (v) => setState(() => condition = v ?? ''),
              ),
              const SizedBox(height: 10),
              Row(
                children: [
                  const Expanded(
                    child: Text(
                      '动作段（留空 = 照抄本招动作段，只换命中编号）',
                      style: TextStyle(
                        fontSize: 12,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                  if (widget.baseSegments.isNotEmpty)
                    TextButton.icon(
                      onPressed: copyBase,
                      icon: const Icon(Icons.copy_all, size: 15),
                      label: const Text('照抄本招', style: TextStyle(fontSize: 12)),
                    ),
                ],
              ),
              for (var i = 0; i < rows.length; i++) buildRow(i),
              TextButton.icon(
                onPressed: () =>
                    setState(() => rows.add(_VariantRow(replayTimes: 2))),
                icon: const Icon(Icons.add, size: 16),
                label: const Text('添加一段（默认卡帧）', style: TextStyle(fontSize: 12)),
              ),
              if (error.isNotEmpty)
                Text(
                  error,
                  style: const TextStyle(fontSize: 12, color: Colors.red),
                ),
              const SizedBox(height: 4),
              const Text(
                '保存后还要点「应用到游戏」才会写进配置包；分支块会新分一份命中编号（skillproid）'
                '并克隆命中属性，不影响原本的无条件动作。点某一段的命中属性编号可以直接改它的'
                '伤害 / 受击动作，改的就是「连招与命中效果」页里那条。',
                style: TextStyle(fontSize: 11, color: Colors.black54),
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
        FilledButton(onPressed: submit, child: const Text('确定')),
      ],
    );
  }

  /// 一段动作：帧区间 + 伤害 + 该段的命中属性（挑模板 / 看编号 / 点开改数值）。
  Widget buildRow(int index) {
    final row = rows[index];
    final id = row.skillproid.trim();
    final pending = id.isNotEmpty && !widget.applied.contains(id);
    return Padding(
      padding: const EdgeInsets.only(top: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(
                flex: 3,
                child: TextFormField(
                  controller: row.nameCtrl,
                  decoration: const InputDecoration(
                    labelText: '动画名',
                    isDense: true,
                  ),
                ),
              ),
              const SizedBox(width: 6),
              SizedBox(
                width: 60,
                child: TextFormField(
                  controller: row.startCtrl,
                  decoration: const InputDecoration(
                    labelText: '起',
                    isDense: true,
                  ),
                  keyboardType: TextInputType.number,
                ),
              ),
              const SizedBox(width: 6),
              SizedBox(
                width: 60,
                child: TextFormField(
                  controller: row.endCtrl,
                  decoration: const InputDecoration(
                    labelText: '止',
                    isDense: true,
                  ),
                  keyboardType: TextInputType.number,
                ),
              ),
              const SizedBox(width: 6),
              SizedBox(
                width: 64,
                child: TextFormField(
                  controller: row.replayCtrl,
                  decoration: const InputDecoration(
                    labelText: '卡帧',
                    isDense: true,
                  ),
                  keyboardType: TextInputType.number,
                ),
              ),
              const SizedBox(width: 6),
              GestureDetector(
                onTap: () => setState(() {
                  final removed = rows.removeAt(index);
                  final id = removed.skillproid.trim();
                  if (id.isNotEmpty) {
                    releaseID(id);
                    overrides.remove(id);
                  }
                  dropped.add(removed);
                }),
                child: const Padding(
                  padding: EdgeInsets.only(left: 6, top: 10),
                  child: Icon(Icons.close, size: 16, color: Colors.deepOrange),
                ),
              ),
            ],
          ),
          Padding(
            padding: const EdgeInsets.only(left: 4, top: 2, bottom: 6),
            child: Row(
              children: [
                if (id.isEmpty)
                  TextButton.icon(
                    onPressed: () => addHit(index),
                    icon: const Icon(Icons.add_circle_outline, size: 14),
                    label: const Text('增加命中属性', style: TextStyle(fontSize: 11)),
                  )
                else ...[
                  ActionChip(
                    avatar: Icon(
                      pending ? Icons.hourglass_top : Icons.gps_fixed,
                      size: 14,
                    ),
                    label: Text(
                      pending ? '命中属性 $id（待应用）' : '命中属性 $id',
                      style: const TextStyle(
                        fontSize: 11,
                        fontFamily: 'monospace',
                      ),
                    ),
                    onPressed: () => editHit(index),
                  ),
                  const SizedBox(width: 6),
                  TextButton(
                    onPressed: () => setState(() {
                      releaseID(id);
                      overrides.remove(id);
                      row.skillproid = '';
                      row.templateSkillproid = '';
                      row.segmentDamage = '';
                      row.replayTimes = 2;
                      row.replayCtrl.text = '2';
                    }),
                    child: const Text('清除', style: TextStyle(fontSize: 11)),
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// 命中属性数值编辑（动作分支里点编号打开的就是它）。
///
/// 字段与「连招与命中效果」页里的命中属性编辑完全同源：同样的 propertyFields、
/// 同样的受击动作选项表、同样的命中参数枚举。保存的是同一份 rule['properties'][id]。
class _HitPropertyDialog extends StatefulWidget {
  const _HitPropertyDialog({
    required this.hitId,
    required this.variant,
    required this.original,
    required this.initial,
    required this.fields,
    required this.effects,
    required this.hitOptions,
    required this.allowed,
  });

  final String hitId;
  final String variant;
  final Map<String, dynamic> original;
  final Map<String, dynamic> initial;
  final List fields;
  final List effects;
  final Map<String, dynamic> hitOptions;
  final Map<String, dynamic> allowed;

  @override
  State<_HitPropertyDialog> createState() => _HitPropertyDialogState();
}

class _HitPropertyDialogState extends State<_HitPropertyDialog> {
  late final Map<String, dynamic> values;
  String reaction = 'original';
  final damageCtrls = <String, TextEditingController>{};
  String error = '';

  @override
  void initState() {
    super.initState();
    values = Map<String, dynamic>.from(widget.initial);
    for (final field in widget.fields) {
      final key = '${field['key']}';
      final current = values[key] ?? widget.original[key];
      if (key == 'SkillDamage' || key == 'SkillEnhanceDamage') {
        damageCtrls[key] = TextEditingController(
          text: values.containsKey(key) ? '$current' : '',
        );
      }
    }
    reaction = widget.initial.isEmpty ? 'original' : 'custom';
  }

  @override
  void dispose() {
    for (final c in damageCtrls.values) {
      c.dispose();
    }
    super.dispose();
  }

  int valueOf(String key) {
    final raw = values[key] ?? widget.original[key] ?? 0;
    return int.tryParse('$raw') ?? 0;
  }

  /// 一个下拉字段的候选项：客户端在用的值都收，不在选项表里的原值也保留。
  List<Map<String, dynamic>> optionsFor(String key, int current) {
    final known = (widget.hitOptions[key] as List? ?? [])
        .map((o) => Map<String, dynamic>.from(o as Map))
        .toList();
    final values = {for (final o in known) o['value'] as int};
    if (!values.contains(current)) {
      known.add({
        'value': current,
        'label': '客户端原生值（未收录音译）',
        'detail': '这个编号在官方配置里用到了，但暂无中文说明，保留原值更安全',
      });
    }
    return known;
  }

  void submit() {
    final out = <String, dynamic>{};
    for (final entry in damageCtrls.entries) {
      final text = entry.value.text.trim();
      if (text.isEmpty) continue;
      final parsed = num.tryParse(text);
      Map<String, dynamic>? field;
      for (final candidate in widget.fields) {
        if ('${candidate['key']}' == entry.key) {
          field = Map<String, dynamic>.from(candidate as Map);
          break;
        }
      }
      if (parsed == null || field == null) {
        setState(() => error = '伤害不是数字');
        return;
      }
      if (parsed < field['min'] || parsed > field['max']) {
        setState(() => error = '${field?['name'] ?? entry.key} 超出范围');
        return;
      }
      out[entry.key] = parsed;
    }
    // Return the complete edited values so restoring defaults also replaces
    // previous overrides, while untouched advanced fields remain present.
    final advancedValues = reaction == 'original' ? widget.original : values;
    advancedValues.forEach((key, value) {
      if (key == 'SkillDamage' || key == 'SkillEnhanceDamage') return;
      final parsed = value is num ? value : num.tryParse('$value');
      if (parsed != null && parsed.isFinite) out[key] = parsed;
    });
    Navigator.pop(context, out);
  }

  @override
  Widget build(BuildContext context) {
    final damageFields = widget.fields
        .where(
          (f) =>
              '${f['key']}' == 'SkillDamage' ||
              '${f['key']}' == 'SkillEnhanceDamage',
        )
        .toList();
    final advanced = widget.fields.where((f) => f['oneshot'] == true).toList();
    final branch = widget.variant.trim().isEmpty
        ? ''
        : ' · 分支 ${widget.variant}';
    return AlertDialog(
      title: Text('命中属性 ${widget.hitId}$branch'),
      content: SizedBox(
        width: 560,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DropdownButtonFormField<String>(
                isExpanded: true,
                initialValue: reaction,
                decoration: const InputDecoration(labelText: '受击动作'),
                items: [
                  const DropdownMenuItem(
                    value: 'original',
                    child: Text('默认（客户端原生）'),
                  ),
                  if (reaction == 'custom')
                    const DropdownMenuItem(
                      value: 'custom',
                      enabled: false,
                      child: Text('自定义受击动作'),
                    ),
                  for (final effect in widget.effects)
                    DropdownMenuItem(
                      value: '${effect['id']}',
                      child: Text(
                        '${effect['id']}' == 'float'
                            ? '上升 / 悬浮'
                            : '${effect['name']}',
                      ),
                    ),
                ],
                onChanged: (v) => setState(() {
                  reaction = v ?? 'original';
                  if (reaction == 'original') {
                    values.removeWhere(
                      (key, _) =>
                          key != 'SkillDamage' && key != 'SkillEnhanceDamage',
                    );
                  } else {
                    for (final effect in widget.effects) {
                      if ('${effect['id']}' == reaction) {
                        values.addAll(
                          Map<String, dynamic>.from(effect['values'] as Map),
                        );
                      }
                    }
                  }
                }),
              ),
              const SizedBox(height: 8),
              Wrap(
                spacing: 12,
                runSpacing: 12,
                children: [
                  for (final field in damageFields)
                    SizedBox(
                      width: 170,
                      child: TextFormField(
                        controller: damageCtrls['${field['key']}'],
                        decoration: InputDecoration(
                          labelText: '${field['name']}',
                          helperText:
                              '默认 ${widget.original['${field['key']}']}',
                        ),
                        keyboardType: TextInputType.number,
                      ),
                    ),
                ],
              ),
              const SizedBox(height: 8),
              ExpansionTile(
                tilePadding: EdgeInsets.zero,
                title: const Text(
                  '击飞参数 / 高级设置',
                  style: TextStyle(fontSize: 13),
                ),
                children: [
                  Wrap(
                    spacing: 12,
                    runSpacing: 12,
                    children: [
                      for (final field in advanced)
                        SizedBox(
                          width: 250,
                          child: DropdownButtonFormField<int>(
                            isExpanded: true,
                            initialValue: valueOf('${field['key']}'),
                            decoration: InputDecoration(
                              labelText: '${field['name']}（${field['key']}）',
                              helperText:
                                  '默认 ${widget.original['${field['key']}']}',
                            ),
                            items: [
                              for (final option in optionsFor(
                                '${field['key']}',
                                valueOf('${field['key']}'),
                              ))
                                DropdownMenuItem<int>(
                                  value: option['value'] as int,
                                  child: Text(
                                    '${option['label']}（${option['value']}）',
                                    overflow: TextOverflow.ellipsis,
                                  ),
                                ),
                            ],
                            onChanged: (v) => setState(() {
                              values['${field['key']}'] = v;
                              reaction = 'custom';
                            }),
                          ),
                        ),
                    ],
                  ),
                ],
              ),
              if (error.isNotEmpty)
                Text(
                  error,
                  style: const TextStyle(fontSize: 12, color: Colors.red),
                ),
              const SizedBox(height: 4),
              const Text(
                '这里改的是「连招与命中效果」页里那条同名命中属性，两处联动。'
                '「清除全部改动」会让它回到客户端原生数值。',
                style: TextStyle(fontSize: 11, color: Colors.black54),
              ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context, <String, dynamic>{}),
          child: const Text('清除全部改动'),
        ),
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        FilledButton(onPressed: submit, child: const Text('确定')),
      ],
    );
  }
}

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
                          if (v == null ||
                              v < widget.minID ||
                              v > widget.maxID) {
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
                  decoration: const InputDecoration(labelText: '备注（可选，仅本地记录）'),
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
  late final TextEditingController glow;

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
    glow = TextEditingController(text: '${widget.initial['glow'] ?? ''}');
  }

  @override
  void dispose() {
    name.dispose();
    icon.dispose();
    description.dispose();
    note.dispose();
    glow.dispose();
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
      'glow': glow.text.trim(),
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
                const Text('编号、供体、子类与模型不可修改；改动在「应用到游戏」后写入配置包。'),
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
                  decoration: const InputDecoration(labelText: '备注（仅本地记录）'),
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
    final suggestedFirst = [...widget.candidates]
      ..sort((a, b) {
        if (a['id'] == widget.suggested) return -1;
        if (b['id'] == widget.suggested) return 1;
        return (a['id'] as int).compareTo(b['id'] as int);
      });
    final matches = suggestedFirst.where((w) {
      final text = query.trim();
      return text.isEmpty || '${w['name']} ${w['id']}'.contains(text);
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
/// 客户端目录选择器：自动列出探测到的客户端 + 允许手填路径。
/// 刻意不依赖原生文件对话框（file_selector 插件在部分部署下会静默失败），
/// 因此武器编辑器与客户端配置页共用同一份实现。
class ClientPickerDialog extends StatefulWidget {
  const ClientPickerDialog({
    super.key,
    required this.current,
    required this.detected,
    this.savedTo = '',
  });

  final String current;
  final List<Map<String, dynamic>> detected;
  final String savedTo;

  @override
  State<ClientPickerDialog> createState() => _ClientPickerDialogState();
}

class _ClientPickerDialogState extends State<ClientPickerDialog> {
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
            if (widget.savedTo.isNotEmpty) ...[
              const SizedBox(height: 2),
              Text(
                '选择后写入 ${widget.savedTo}',
                style: TextStyle(
                  fontSize: 11,
                  color: Theme.of(context).hintColor,
                ),
              ),
            ],
            const SizedBox(height: 2),
            Expanded(
              child: widget.detected.isEmpty
                  ? const Center(child: Text('没有自动找到客户端，请在下面直接填路径'))
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
                                      : '$directory\nconfig.spf2  ${hash.length > 12 ? hash.substring(0, 12) : hash}…'),
                            style: TextStyle(
                              fontSize: 11,
                              color: valid ? null : Colors.deepOrange.shade900,
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
                              final fx = (s['effects'] as List? ?? []).join(
                                '、',
                              );
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
                                isThreeLine: ids.isNotEmpty || fx.isNotEmpty,
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
              const Text('选择导出范围：', style: TextStyle(fontSize: 12)),
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
    required this.onCommit,
    required this.onAddProperty,
    required this.isCurrent,
    super.key,
  });

  final bool Function() isCurrent;
  final void Function(Map<String, dynamic>) onCommit;
  final String Function(String, List<Map<String, dynamic>>) onAddProperty;
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
  Map<String, dynamic>? templateHit;
  bool saving = false;

  @override
  void initState() {
    super.initState();
    action = TextEditingController(text: '${widget.initial['action'] ?? ''}');
    property = TextEditingController(
      text: '${widget.initial['property_id'] ?? ''}',
    );
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
      builder: (_) =>
          _RemapTemplateDialog(weapons: widget.weapons, self: widget.weaponId),
    );
    if (picked == null || !mounted || !widget.isCurrent()) return;
    try {
      final r = Map<String, dynamic>.from(
        await widget.api({
          'operation': 'weapon_template_resolve',
          'template_weapon': picked['weapon'],
          'template_stage': picked['state'],
        }),
      );
      if (!mounted || !widget.isCurrent()) return;
      setState(() {
        action.text = '${r['action'] ?? ''}';
        property.text = '${r['property_id'] ?? ''}';
        templateHit = r['hit'] is Map
            ? Map<String, dynamic>.from(r['hit'] as Map)
            : null;
      });
    } catch (e) {
      if (mounted && widget.isCurrent()) {
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
    } catch (_) {
      return;
    }
    if (!mounted || !widget.isCurrent()) return;
    final properties = [
      for (final p in (catalog['properties'] as List? ?? []))
        Map<String, dynamic>.from(p as Map),
    ];
    final template = await showDialog<String>(
      context: context,
      builder: (_) => _PropertyPickerDialog(properties: properties),
    );
    if (template == null || !mounted || !widget.isCurrent()) return;
    try {
      final newId = widget.onAddProperty(template, properties);
      if (!mounted) return;
      final templateRow = properties.firstWhere(
        (row) => '${row['id']}' == template,
      );
      setState(() {
        property.text = newId;
        templateHit = {
          'id': newId,
          'buff': templateRow['buff'] ?? '0',
          'values': Map<String, dynamic>.from(
            templateRow['values'] as Map? ?? const <String, dynamic>{},
          ),
        };
      });
    } catch (e) {
      if (mounted && widget.isCurrent()) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('$e')));
      }
    }
  }

  Future<void> commit({bool clear = false}) async {
    setState(() => saving = true);
    try {
      widget.onCommit({
        'action': clear ? '' : action.text.trim(),
        'property_id': clear ? '' : property.text.trim(),
        'label': clear ? '' : label.text.trim(),
        if (!clear && templateHit != null) 'hit': templateHit,
        if (!clear && templateHit != null)
          'template_property_id': '${templateHit!['id']}',
      });
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
    } catch (e) {
      if (mounted) {
        setState(() => saving = false);
        if (widget.isCurrent()) {
          ScaffoldMessenger.of(context)
              .showSnackBar(SnackBar(content: Text('$e')));
        }
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

/// 特效的绑定方式（bindtype）。游戏里没有公开的枚举名，这张表是按全库 9000+ 处
/// <Effect> / <HitEffect> 的实际用法归纳出来的：6 几乎只配 bindindex=0（出招瞬间在
/// 武器原点），2 几乎只出现在 <HitEffect> 且带具体骨骼号。所以按“绑在哪”来理解。
/// bindindex 是骨骼编号（客户端骨架实测 27 根，取值 0–26）。
const effectBindTypes = <String, String>{
  '': '默认（走 bindbone / custombind）',
  '1': '1 · 角色骨骼',
  '2': '2 · 命中部位骨骼（HitEffect 常见）',
  '3': '3 · 武器挂点',
  '4': '4 · 手部',
  '5': '5 · 少见',
  '6': '6 · 武器原点（单帧出招最常用）',
  '10': '10 · 特殊',
  '19': '19 · 特殊',
};

/// 特效缩略图：后端给的是 data URI（PNG base64）；读不到图时给占位。
Widget _effectThumb(String dataUri, double size) {
  final placeholder = Container(
    width: size,
    height: size,
    decoration: BoxDecoration(
      color: Colors.black12,
      borderRadius: BorderRadius.circular(4),
    ),
    child: Icon(Icons.image_not_supported_outlined, size: size * 0.45),
  );
  if (dataUri.isEmpty) return placeholder;
  try {
    final bytes = base64Decode(dataUri.split(',').last);
    return ClipRRect(
      borderRadius: BorderRadius.circular(4),
      child: Image.memory(
        bytes,
        width: size,
        height: size,
        fit: BoxFit.contain,
        errorBuilder: (_, __, ___) => Container(
          width: size,
          height: size,
          decoration: BoxDecoration(
            color: Colors.black12,
            borderRadius: BorderRadius.circular(4),
          ),
          child: Icon(Icons.broken_image_outlined, size: size * 0.45),
        ),
      ),
    );
  } catch (_) {
    return placeholder;
  }
}

/// 武器特效：预览登记了什么、招式引用了什么，并允许增删登记。
class _EffectLedgerDialog extends StatefulWidget {
  const _EffectLedgerDialog({
    required this.title,
    required this.editable,
    required this.view,
    required this.onSubmit,
  });

  final String title;
  final bool editable;
  final Map<String, dynamic> view;
  final Future<dynamic> Function(List<Map<String, dynamic>> rows) onSubmit;

  @override
  State<_EffectLedgerDialog> createState() => _EffectLedgerDialogState();
}

class _EffectLedgerDialogState extends State<_EffectLedgerDialog> {
  late List<Map<String, dynamic>> rows;
  final idInput = TextEditingController();
  final fileInput = TextEditingController();
  bool busy = false;
  String message = '';

  @override
  void initState() {
    super.initState();
    rows = [
      for (final entry in (widget.view['registered'] as List? ?? []))
        {
          'effect_id': '${(entry as Map)['effect_id']}',
          'file': '${entry['file']}',
        },
    ];
  }

  @override
  void dispose() {
    idInput.dispose();
    fileInput.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final thumbs = <String, String>{};
    for (final entry in (widget.view['registered'] as List? ?? [])) {
      final map = Map<String, dynamic>.from(entry as Map);
      final data = '${map['thumbnail'] ?? ''}';
      if (data.isNotEmpty) thumbs['${map['effect_id']}'] = data;
    }
    final unregistered = (widget.view['unregistered'] as List? ?? [])
        .map((e) => '$e')
        .toList();
    final references = widget.view['references'] as List? ?? [];
    return AlertDialog(
      title: Text('${widget.title} · 模型特效（装备时预加载）'),
      content: SizedBox(
        width: 740,
        height: 470,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              widget.editable
                  ? '这是**武器模型**要预加载的特效登记（acteffect.xml）。招式里引用的特效若不在'
                        '这张表里就不会加载；这里只写编辑集，点「应用到游戏」才进配置包。'
                  : '这是武器模型的特效登记。原有武器只能预览，编辑只对自建武器开放。',
              style: const TextStyle(fontSize: 12),
            ),
            const SizedBox(height: 4),
            Text(
              '招式引用 ${references.length} 处 · 公共登记 ${widget.view['common_count'] ?? 0} 条',
              style: TextStyle(
                fontSize: 12,
                color: Theme.of(context).hintColor,
              ),
            ),
            if (unregistered.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 6),
                child: Text(
                  '招式引用了但尚未登记：${unregistered.join('、')}',
                  style: const TextStyle(
                    fontSize: 12,
                    color: Colors.deepOrange,
                  ),
                ),
              ),
            const SizedBox(height: 8),
            if (thumbs.isNotEmpty)
              SizedBox(
                height: 50,
                child: ListView(
                  scrollDirection: Axis.horizontal,
                  children: [
                    for (final entry in thumbs.entries) ...[
                      Tooltip(
                        message: entry.key,
                        child: _effectThumb(entry.value, 44),
                      ),
                      const SizedBox(width: 6),
                    ],
                  ],
                ),
              ),
            Expanded(
              child: rows.isEmpty
                  ? const Center(child: Text('这个武器还没有特效登记'))
                  : ListView.builder(
                      itemCount: rows.length,
                      itemBuilder: (context, index) {
                        final row = rows[index];
                        final id = '${row['effect_id']}';
                        return ListTile(
                          dense: true,
                          leading: _effectThumb(thumbs[id] ?? '', 32),
                          title: Text(id),
                          subtitle: Text('资源 ${row['file']}'),
                          trailing: widget.editable
                              ? IconButton(
                                  tooltip: '删除登记',
                                  icon: const Icon(
                                    Icons.delete_outline,
                                    size: 18,
                                  ),
                                  onPressed: busy
                                      ? null
                                      : () => setState(
                                          () => rows.removeAt(index),
                                        ),
                                )
                              : null,
                        );
                      },
                    ),
            ),
            if (widget.editable) ...[
              const Divider(height: 12),
              Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: idInput,
                      decoration: const InputDecoration(
                        labelText: '特效编号',
                        isDense: true,
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: TextField(
                      controller: fileInput,
                      decoration: const InputDecoration(
                        labelText: '资源文件名（留空同编号）',
                        isDense: true,
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  TextButton(
                    onPressed: busy ? null : addRow,
                    child: const Text('添加'),
                  ),
                ],
              ),
            ],
            if (message.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 6),
                child: Text(message, style: const TextStyle(fontSize: 12)),
              ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: busy ? null : () => Navigator.pop(context, false),
          child: const Text('关闭'),
        ),
        if (widget.editable)
          FilledButton(
            onPressed: busy ? null : save,
            child: Text(busy ? '保存中…' : '保存登记'),
          ),
      ],
    );
  }

  void addRow() {
    final id = idInput.text.trim();
    if (id.isEmpty) {
      setState(() => message = '请填特效编号');
      return;
    }
    final file = fileInput.text.trim().isEmpty ? id : fileInput.text.trim();
    setState(() {
      rows.removeWhere((row) => '${row['effect_id']}' == id);
      rows.add({'effect_id': id, 'file': file});
      idInput.clear();
      fileInput.clear();
      message = '';
    });
  }

  Future<void> save() async {
    setState(() {
      busy = true;
      message = '';
    });
    try {
      await widget.onSubmit(rows);
      if (!mounted) return;
      Navigator.pop(context, true);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          message = '保存失败：$e';
        });
      }
    }
  }
}

/// 单条招式特效：改帧号（区间）与绑定方式 / 骨骼号。
class _EffectRowDialog extends StatefulWidget {
  const _EffectRowDialog({required this.row});

  final Map<String, dynamic> row;

  @override
  State<_EffectRowDialog> createState() => _EffectRowDialogState();
}

class _EffectRowDialogState extends State<_EffectRowDialog> {
  late String kind;
  late String bindType;
  late final TextEditingController idInput;
  late final TextEditingController startInput;
  late final TextEditingController endInput;
  late final TextEditingController bindIndexInput;
  late final TextEditingController breakInput;

  @override
  void initState() {
    super.initState();
    kind = '${widget.row['kind'] ?? 'effect'}';
    bindType = '${widget.row['bind_type'] ?? ''}';
    idInput = TextEditingController(text: '${widget.row['effect_id'] ?? ''}');
    startInput = TextEditingController(text: '${widget.row['start'] ?? 0}');
    endInput = TextEditingController(text: '${widget.row['end'] ?? 0}');
    bindIndexInput = TextEditingController(
      text: '${widget.row['bind_index'] ?? ''}',
    );
    breakInput = TextEditingController(text: '${widget.row['break'] ?? ''}');
  }

  @override
  void dispose() {
    idInput.dispose();
    startInput.dispose();
    endInput.dispose();
    bindIndexInput.dispose();
    breakInput.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('编辑这条特效'),
      content: SizedBox(
        width: 460,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                DropdownButton<String>(
                  value: kind,
                  items: const [
                    DropdownMenuItem(value: 'effect', child: Text('单帧特效')),
                    DropdownMenuItem(value: 'hit', child: Text('命中特效')),
                  ],
                  onChanged: (value) =>
                      setState(() => kind = value ?? 'effect'),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: TextField(
                    key: const ValueKey('effect-row-id'),
                    controller: idInput,
                    decoration: const InputDecoration(
                      labelText: '特效编号',
                      isDense: true,
                    ),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Row(
              children: [
                SizedBox(
                  width: 90,
                  child: TextField(
                    key: const ValueKey('effect-row-start'),
                    controller: startInput,
                    decoration: const InputDecoration(
                      labelText: '起始帧',
                      isDense: true,
                    ),
                    keyboardType: TextInputType.number,
                  ),
                ),
                const SizedBox(width: 8),
                SizedBox(
                  width: 90,
                  child: TextField(
                    key: const ValueKey('effect-row-end'),
                    controller: endInput,
                    decoration: const InputDecoration(
                      labelText: '结束帧',
                      isDense: true,
                    ),
                    keyboardType: TextInputType.number,
                  ),
                ),
                const SizedBox(width: 8),
                SizedBox(
                  width: 82,
                  child: TextField(
                    key: const ValueKey('effect-row-bind-index'),
                    controller: bindIndexInput,
                    decoration: const InputDecoration(
                      labelText: '骨骼号',
                      isDense: true,
                    ),
                    keyboardType: TextInputType.number,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            DropdownButtonFormField<String>(
              initialValue: bindType,
              decoration: const InputDecoration(
                labelText: 'bindtype（特效绑在哪）',
                isDense: true,
              ),
              items: [
                for (final entry in effectBindTypes.entries)
                  DropdownMenuItem(value: entry.key, child: Text(entry.value)),
              ],
              onChanged: (value) => setState(() => bindType = value ?? ''),
            ),
            const SizedBox(height: 6),
            TextField(
              key: const ValueKey('effect-row-break'),
              controller: breakInput,
              decoration: const InputDecoration(
                labelText: 'break（可空）',
                isDense: true,
              ),
            ),
            const SizedBox(height: 6),
            Text(
              'bindindex 是骨骼编号（0–26）；bindtype 留空表示沿用原来的写法。',
              style: TextStyle(
                fontSize: 11,
                color: Theme.of(context).hintColor,
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
          onPressed: () {
            final start = int.tryParse(startInput.text.trim()) ?? 0;
            var end = int.tryParse(endInput.text.trim()) ?? 0;
            if (kind == 'effect') end = start;
            Navigator.pop(context, {
              'kind': kind,
              'effect_id': idInput.text.trim(),
              'start': start,
              'end': end,
              'bind_type': bindType,
              'bind_index': bindIndexInput.text.trim(),
              'break': breakInput.text.trim(),
            });
          },
          child: const Text('确定'),
        ),
      ],
    );
  }
}

/// 招式特效：预览并编辑某个状态动作块里的特效节点。
class _StageEffectDialog extends StatefulWidget {
  const _StageEffectDialog({
    required this.title,
    required this.editable,
    required this.rows,
    required this.thumbs,
    required this.onSubmit,
  });

  final String title;
  final bool editable;
  final List<Map<String, dynamic>> rows;
  final Map<String, String> thumbs;
  final Future<dynamic> Function(List<Map<String, dynamic>> rows) onSubmit;

  @override
  State<_StageEffectDialog> createState() => _StageEffectDialogState();
}

class _StageEffectDialogState extends State<_StageEffectDialog> {
  late List<Map<String, dynamic>> edits;
  final idInput = TextEditingController();
  final startInput = TextEditingController(text: '0');
  final endInput = TextEditingController(text: '0');
  final bindIndexInput = TextEditingController(text: '0');
  final breakInput = TextEditingController();
  String kind = 'effect';
  String bindType = '';
  bool busy = false;
  String message = '';

  @override
  void initState() {
    super.initState();
    edits = [
      for (final row in widget.rows)
        {
          'kind': '${row['kind'] ?? 'effect'}',
          'effect_id': '${row['effect_id']}',
          'start': row['start'] ?? 0,
          'end': row['end'] ?? 0,
          'bind_type': '${row['bind_type'] ?? ''}',
          'bind_index': '${row['bind_index'] ?? ''}',
          'break': '${row['break'] ?? ''}',
        },
    ];
  }

  @override
  void dispose() {
    idInput.dispose();
    startInput.dispose();
    endInput.dispose();
    bindIndexInput.dispose();
    breakInput.dispose();
    super.dispose();
  }

  String _label(Map<String, dynamic> row) {
    final id = '${row['effect_id']}';
    final start = row['start'] ?? 0;
    final end = row['end'] ?? 0;
    if ('${row['kind']}' == 'hit') {
      return '命中特效 $id · 第 $start–$end 帧';
    }
    return '特效 $id · 第 $start 帧';
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text('${widget.title} · 招式特效'),
      content: SizedBox(
        width: 680,
        height: 440,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              widget.editable
                  ? '这里改的是这个状态动作块里的 <Effect>（单帧）与 <HitEffect>（命中区间）。'
                        '共用动作块会先克隆成这把武器独占，其它武器和原有招式不受影响；'
                        '列表即最终结果（空 = 该招式没有特效）。'
                  : '原有武器只能预览特效；编辑只对自建武器开放。',
              style: const TextStyle(fontSize: 12),
            ),
            const SizedBox(height: 8),
            Expanded(
              child: edits.isEmpty
                  ? const Center(child: Text('这个招式没有特效'))
                  : ListView.builder(
                      itemCount: edits.length,
                      itemBuilder: (context, index) {
                        final row = edits[index];
                        final id = '${row['effect_id']}';
                        final bind = '${row['bind_type'] ?? ''}';
                        return ListTile(
                          dense: true,
                          leading: _effectThumb(widget.thumbs[id] ?? '', 32),
                          title: Text(_label(row)),
                          subtitle: Text(
                            '绑定 ${bind.isEmpty ? '默认' : (effectBindTypes[bind] ?? bind)}'
                            ' · 骨骼 ${row['bind_index'] ?? ''}',
                            style: const TextStyle(fontSize: 11),
                          ),
                          trailing: widget.editable
                              ? Row(
                                  mainAxisSize: MainAxisSize.min,
                                  children: [
                                    IconButton(
                                      tooltip: '改帧号 / 绑定方式',
                                      icon: const Icon(
                                        Icons.edit_outlined,
                                        size: 18,
                                      ),
                                      onPressed: busy
                                          ? null
                                          : () => editRow(index),
                                    ),
                                    IconButton(
                                      tooltip: '删除',
                                      icon: const Icon(
                                        Icons.delete_outline,
                                        size: 18,
                                      ),
                                      onPressed: busy
                                          ? null
                                          : () => setState(
                                              () => edits.removeAt(index),
                                            ),
                                    ),
                                  ],
                                )
                              : null,
                        );
                      },
                    ),
            ),
            if (widget.editable) ...[
              const Divider(height: 12),
              SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                child: Row(
                  children: [
                    DropdownButton<String>(
                      value: kind,
                      items: const [
                        DropdownMenuItem(value: 'effect', child: Text('单帧特效')),
                        DropdownMenuItem(value: 'hit', child: Text('命中特效')),
                      ],
                      onChanged: busy
                          ? null
                          : (value) => setState(() => kind = value ?? 'effect'),
                    ),
                    const SizedBox(width: 8),
                    SizedBox(
                      width: 120,
                      child: TextField(
                        controller: idInput,
                        decoration: const InputDecoration(
                          labelText: '特效编号',
                          isDense: true,
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    SizedBox(
                      width: 84,
                      child: TextField(
                        controller: startInput,
                        decoration: const InputDecoration(
                          labelText: '起始帧',
                          isDense: true,
                        ),
                        keyboardType: TextInputType.number,
                      ),
                    ),
                    const SizedBox(width: 8),
                    SizedBox(
                      width: 84,
                      child: TextField(
                        controller: endInput,
                        decoration: const InputDecoration(
                          labelText: '结束帧',
                          isDense: true,
                        ),
                        keyboardType: TextInputType.number,
                      ),
                    ),
                    const SizedBox(width: 8),
                    SizedBox(
                      width: 72,
                      child: TextField(
                        controller: bindIndexInput,
                        decoration: const InputDecoration(
                          labelText: '骨骼号',
                          isDense: true,
                        ),
                        keyboardType: TextInputType.number,
                      ),
                    ),
                    const SizedBox(width: 8),
                    SizedBox(
                      width: 90,
                      child: TextField(
                        controller: breakInput,
                        decoration: const InputDecoration(
                          labelText: 'break',
                          isDense: true,
                        ),
                      ),
                    ),
                    const SizedBox(width: 8),
                    DropdownButton<String>(
                      value: bindType,
                      items: [
                        for (final entry in effectBindTypes.entries)
                          DropdownMenuItem(
                            value: entry.key,
                            child: Text(
                              entry.value,
                              style: const TextStyle(fontSize: 12),
                            ),
                          ),
                      ],
                      onChanged: busy
                          ? null
                          : (value) => setState(() => bindType = value ?? ''),
                    ),
                    const SizedBox(width: 8),
                    TextButton(
                      onPressed: busy ? null : addRow,
                      child: const Text('添加'),
                    ),
                  ],
                ),
              ),
              const SizedBox(height: 4),
              Text(
                'bindtype = 特效绑在哪（2 多用于命中部位、6 多用于出招瞬间的武器特效）；'
                'bindindex = 骨骼编号 0–26。已有条目点右侧铅笔单独改。',
                style: TextStyle(
                  fontSize: 11,
                  color: Theme.of(context).hintColor,
                ),
              ),
            ],
            if (message.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 6),
                child: Text(message, style: const TextStyle(fontSize: 12)),
              ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: busy ? null : () => Navigator.pop(context, false),
          child: const Text('关闭'),
        ),
        if (widget.editable)
          FilledButton(
            onPressed: busy ? null : save,
            child: Text(busy ? '保存中…' : '保存招式特效'),
          ),
      ],
    );
  }

  void addRow() {
    final id = idInput.text.trim();
    if (id.isEmpty) {
      setState(() => message = '请填特效编号');
      return;
    }
    final start = int.tryParse(startInput.text.trim()) ?? 0;
    var end = int.tryParse(endInput.text.trim()) ?? 0;
    if (kind == 'effect') {
      end = start;
    } else if (end < start) {
      setState(() => message = '结束帧不能小于起始帧');
      return;
    }
    setState(() {
      edits.add({
        'kind': kind,
        'effect_id': id,
        'start': start,
        'end': end,
        'bind_type': bindType,
        'bind_index': bindIndexInput.text.trim(),
        'break': breakInput.text.trim(),
      });
      idInput.clear();
      message = '';
    });
  }

  Future<void> editRow(int index) async {
    final updated = await showDialog<Map<String, dynamic>>(
      context: context,
      builder: (_) => _EffectRowDialog(row: edits[index]),
    );
    if (updated == null || !mounted) return;
    setState(() => edits[index] = updated);
  }

  Future<void> save() async {
    setState(() {
      busy = true;
      message = '';
    });
    try {
      await widget.onSubmit(edits);
      if (!mounted) return;
      Navigator.pop(context, true);
    } catch (e) {
      if (mounted) {
        setState(() {
          busy = false;
          message = '保存失败：$e';
        });
      }
    }
  }
}
