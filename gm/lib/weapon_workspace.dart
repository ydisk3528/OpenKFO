import 'dart:convert';

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

/// 当前武器的完整编辑工作区。
///
/// 编辑仅保存在内存，保存与应用传递完整快照。
/// 所有内容保持 JSON-compatible，方便后续原子写入
/// runtime-local/weapon-config/workspaces/weapon-<id>.json。
class WeaponWorkspace {
  WeaponWorkspace({
    required this.weapon,
    required this.data,
    required this.rules,
    required this.comboChain,
    required this.comboDeadEnds,
    required this.frameSwitches,
    required this.frameEdits,
    required this.frameSaved,
    required this.counters,
    required this.counterEdits,
    required this.counterSaved,
    required this.blockElements,
    required this.blockElementsSaved,
    required this.blockElementsEdit,
    required this.variants,
    required this.variantsSaved,
    required this.variantsEdit,
    required this.variantBases,
    required this.variantOccupiedIDs,
    required this.stageTracks,
    required this.scopeSaved,
    required this.comboRuleInfo,
    required this.comboRuleMaxDraft,
    required this.comboRuleBlackDraft,
    required this.comboRuleWhiteDraft,
    required this.extra,
    this.effectRows,
    required this.stageEffects,
    this.remaps = const {},
    this.cleared = const {},
    this.extraProperties = const {},
    this.hitProperties = const {},
  });

  final Map<String, dynamic> weapon;
  final Map<String, dynamic> data;
  final List<Map<String, dynamic>> rules;
  final List<Map<String, String>> comboChain;
  final List<Map<String, String>> comboDeadEnds;
  final List<Map<String, dynamic>> frameSwitches;
  final Map<String, List<Map<String, dynamic>>> frameEdits;
  final Map<String, List<Map<String, dynamic>>> frameSaved;
  final List<Map<String, dynamic>> counters;
  final Map<String, Map<String, dynamic>?> counterEdits;
  final Map<String, Map<String, dynamic>?> counterSaved;
  final List<Map<String, dynamic>> blockElements;
  final Map<String, Map<String, List<Map<String, dynamic>>>> blockElementsSaved;
  final Map<String, Map<String, List<Map<String, dynamic>>>> blockElementsEdit;
  final Map<String, List<Map<String, dynamic>>> variants;
  final Map<String, List<Map<String, dynamic>>> variantsSaved;
  final Map<String, List<Map<String, dynamic>>> variantsEdit;
  final Map<String, List<Map<String, dynamic>>> variantBases;
  final Set<String> variantOccupiedIDs;
  final Map<String, Map<String, dynamic>> stageTracks;
  final Map<String, Map<String, List<Map<String, dynamic>>>> scopeSaved;
  final Map<String, dynamic> comboRuleInfo;
  final List<Map<String, dynamic>> comboRuleMaxDraft;
  final List<Map<String, dynamic>> comboRuleBlackDraft;
  final List<Map<String, dynamic>> comboRuleWhiteDraft;
  final Map<String, dynamic> extra;

  /// null means the author did not override the inherited registration.
  final List<Map<String, dynamic>>? effectRows;

  /// State -> authoritative effect list. An empty list removes that state's effects.
  final Map<String, List<Map<String, dynamic>>> stageEffects;

  final Map<String, dynamic> remaps;
  final Map<String, dynamic> cleared;
  final Map<String, dynamic> extraProperties;

  /// Canonical hit-property objects shared by variants and combo rules.
  /// Legacy fields remain serialized for backward compatibility.
  final Map<String, dynamic> hitProperties;

  int get weaponID => (weapon['id'] as num).toInt();

  /// Creates an isolated snapshot from the page state.
  /// No nested Map/List from the page is retained by the workspace.
  factory WeaponWorkspace.fromPage({
    required Map<String, dynamic> weapon,
    required Map<String, dynamic> data,
    required List<Map<String, dynamic>> rules,
    required List<Map<String, String>> comboChain,
    required List<Map<String, String>> comboDeadEnds,
    required List<Map<String, dynamic>> frameSwitches,
    required Map<String, List<Map<String, dynamic>>> frameEdits,
    required Map<String, List<Map<String, dynamic>>> frameSaved,
    required List<Map<String, dynamic>> counters,
    required Map<String, Map<String, dynamic>?> counterEdits,
    required Map<String, Map<String, dynamic>?> counterSaved,
    required List<Map<String, dynamic>> blockElements,
    required Map<String, Map<String, List<Map<String, dynamic>>>>
    blockElementsSaved,
    required Map<String, Map<String, List<Map<String, dynamic>>>>
    blockElementsEdit,
    required Map<String, List<Map<String, dynamic>>> variants,
    required Map<String, List<Map<String, dynamic>>> variantsSaved,
    required Map<String, List<Map<String, dynamic>>> variantsEdit,
    required Map<String, List<Map<String, dynamic>>> variantBases,
    required Set<String> variantOccupiedIDs,
    required Map<String, Map<String, dynamic>> stageTracks,
    required Map<String, Map<String, List<Map<String, dynamic>>>> scopeSaved,
    required Map<String, dynamic> comboRuleInfo,
    required List<Map<String, dynamic>> comboRuleMaxDraft,
    required List<Map<String, dynamic>> comboRuleBlackDraft,
    required List<Map<String, dynamic>> comboRuleWhiteDraft,
    Map<String, dynamic> extra = const {},
    List<Map<String, dynamic>>? effectRows,
    Map<String, List<Map<String, dynamic>>> stageEffects = const {},
    Map<String, dynamic> remaps = const {},
    Map<String, dynamic> cleared = const {},
    Map<String, dynamic> extraProperties = const {},
    Map<String, dynamic>? hitProperties,
  }) {
    final pageValues = <String, dynamic>{
      for (final stage in (weapon['stages'] as List? ?? const []))
        for (final hit in ((stage as Map)['hits'] as List? ?? const []))
          '${(hit as Map)['id']}': _ruleValues(hit),
    };
    for (final rule in rules) {
      for (final entry in ((rule['properties'] as Map?) ?? const {}).entries) {
        pageValues['${entry.key}'] = {
          ...?pageValues['${entry.key}'] as Map?,
          ..._ruleValues(entry.value),
        };
      }
    }
    for (final entry in (hitProperties ?? const <String, dynamic>{}).entries) {
      pageValues[entry.key] = {
        ...?pageValues[entry.key] as Map?,
        ..._ruleValues(entry.value),
      };
    }
    final sharedHitProperties = _canonicalHitProperties(
      weapon,
      variants,
      variantsSaved,
      variantsEdit,
      pageValues,
      hitProperties,
    );
    // JSON round-tripping gives every nested collection a concrete dynamic type
    // and guarantees that no mutable page object is retained.
    final payload = {
      'weapon': weapon,
      'data': data,
      'rules': [
        for (final rule in rules)
          {
            ...rule,
            if (rule['properties'] is Map)
              'properties': {
                for (final entry in (rule['properties'] as Map).entries)
                  '${entry.key}': _ruleValues(entry.value),
              },
          },
      ],
      'combo_chain': comboChain,
      'combo_dead_ends': comboDeadEnds,
      'frame_switches': frameSwitches,
      'frame_edits': frameEdits,
      'frame_saved': frameSaved,
      'counters': counters,
      'counter_edits': counterEdits,
      'counter_saved': counterSaved,
      'block_elements': blockElements,
      'block_elements_saved': blockElementsSaved,
      'block_elements_edit': blockElementsEdit,
      'variants': variants,
      'variants_saved': variantsSaved,
      'variants_edit': variantsEdit,
      'variant_bases': variantBases,
      'variant_occupied_ids': variantOccupiedIDs.toList(),
      'stage_tracks': stageTracks,
      'scope_saved': scopeSaved,
      'combo_rule_info': comboRuleInfo,
      'combo_rule_max_draft': comboRuleMaxDraft,
      'combo_rule_black_draft': comboRuleBlackDraft,
      'combo_rule_white_draft': comboRuleWhiteDraft,
      'extra': extra,
      if (effectRows != null) 'effect_rows': effectRows,
      'stage_effects': stageEffects,
      'remaps': remaps,
      'cleared': cleared,
      'extra_properties': extraProperties,
      'hit_properties': sharedHitProperties,
    };
    return WeaponWorkspace.fromJson(
      jsonDecode(jsonEncode(payload)) as Map<String, dynamic>,
    );
  }

  Map<String, dynamic> toJson() => {
    'schema_version': 1,
    'weapon': _deepCopy(weapon),
    'data': _deepCopy(data),
    'rules': _deepCopy(rules),
    'combo_chain': _deepCopy(comboChain),
    'combo_dead_ends': _deepCopy(comboDeadEnds),
    'frame_switches': _deepCopy(frameSwitches),
    'frame_edits': _deepCopy(frameEdits),
    'frame_saved': _deepCopy(frameSaved),
    'counters': _deepCopy(counters),
    'counter_edits': _deepCopy(counterEdits),
    'counter_saved': _deepCopy(counterSaved),
    'block_elements': _deepCopy(blockElements),
    'block_elements_saved': _deepCopy(blockElementsSaved),
    'block_elements_edit': _deepCopy(blockElementsEdit),
    'variants': _normalizeVariants(variants),
    'variants_saved': _normalizeVariants(variantsSaved),
    'variants_edit': _normalizeVariants(variantsEdit),
    'variant_bases': _deepCopy(variantBases),
    'variant_occupied_ids': variantOccupiedIDs.toList()..sort(),
    'stage_tracks': _deepCopy(stageTracks),
    'scope_saved': _deepCopy(scopeSaved),
    'combo_rule_info': _deepCopy(comboRuleInfo),
    'combo_rule_max_draft': _deepCopy(comboRuleMaxDraft),
    'combo_rule_black_draft': _deepCopy(comboRuleBlackDraft),
    'combo_rule_white_draft': _deepCopy(comboRuleWhiteDraft),
    'extra': _deepCopy(extra),
    if (effectRows != null) 'effect_rows': _deepCopy(effectRows),
    'stage_effects': _deepCopy(stageEffects),
    'remaps': _deepCopy(remaps),
    'cleared': _deepCopy(cleared),
    'extra_properties': _deepCopy(extraProperties),
    'hit_properties': _deepCopy(hitProperties),
  };

  String encode() => jsonEncode(toJson());

  factory WeaponWorkspace.fromJson(Map<String, dynamic> json) {
    json = Map<String, dynamic>.from(_deepCopy(json) as Map);
    final baseline = json['data'] as Map? ?? const {};
    List<Map<String, dynamic>> list(String key) => [
      for (final value in (json[key] as List? ?? const []))
        Map<String, dynamic>.from(value as Map),
    ];
    Map<String, List<Map<String, dynamic>>> nestedList(String key) => {
      for (final entry in (json[key] as Map? ?? const {}).entries)
        '${entry.key}': [
          for (final value in (entry.value as List? ?? const []))
            Map<String, dynamic>.from(value as Map),
        ],
    };
    Map<String, Map<String, dynamic>?> nullableMap(String key) => {
      for (final entry in (json[key] as Map? ?? const {}).entries)
        '${entry.key}': entry.value == null
            ? null
            : Map<String, dynamic>.from(entry.value as Map),
    };
    Map<String, Map<String, List<Map<String, dynamic>>>> grouped(String key) =>
        {
          for (final entry in (json[key] as Map? ?? const {}).entries)
            '${entry.key}': {
              for (final group in (entry.value as Map? ?? const {}).entries)
                '${group.key}': [
                  for (final value in (group.value as List? ?? const []))
                    Map<String, dynamic>.from(value as Map),
                ],
            },
        };
    return WeaponWorkspace(
      weapon: Map<String, dynamic>.from(json['weapon'] as Map),
      data: Map<String, dynamic>.from(json['data'] as Map? ?? const {}),
      rules: list('rules'),
      comboChain: [
        for (final value in (json['combo_chain'] as List? ?? const []))
          Map<String, String>.from(value as Map),
      ],
      comboDeadEnds: [
        for (final value in (json['combo_dead_ends'] as List? ?? const []))
          Map<String, String>.from(value as Map),
      ],
      frameSwitches: list('frame_switches'),
      frameEdits: nestedList('frame_edits'),
      frameSaved: nestedList('frame_saved'),
      counters: list('counters'),
      counterEdits: nullableMap('counter_edits'),
      counterSaved: nullableMap('counter_saved'),
      blockElements: list('block_elements'),
      blockElementsSaved: grouped('block_elements_saved'),
      blockElementsEdit: grouped('block_elements_edit'),
      variants: _normalizeVariants(nestedList('variants')),
      variantsSaved: _normalizeVariants(nestedList('variants_saved')),
      variantsEdit: _normalizeVariants(nestedList('variants_edit')),
      variantBases: nestedList('variant_bases'),
      variantOccupiedIDs: {
        for (final value in (json['variant_occupied_ids'] as List? ?? const []))
          '$value',
      },
      stageTracks: {
        for (final entry in (json['stage_tracks'] as Map? ?? const {}).entries)
          '${entry.key}': Map<String, dynamic>.from(entry.value as Map),
      },
      scopeSaved: grouped('scope_saved'),
      comboRuleInfo: Map<String, dynamic>.from(
        json['combo_rule_info'] as Map? ?? const {},
      ),
      comboRuleMaxDraft: list('combo_rule_max_draft'),
      comboRuleBlackDraft: list('combo_rule_black_draft'),
      comboRuleWhiteDraft: list('combo_rule_white_draft'),
      extra: Map<String, dynamic>.from(json['extra'] as Map? ?? const {}),
      remaps: Map<String, dynamic>.from(
        json['remaps'] as Map? ?? baseline['remaps'] as Map? ?? const {},
      ),
      cleared: Map<String, dynamic>.from(
        json['cleared'] as Map? ?? baseline['cleared'] as Map? ?? const {},
      ),
      extraProperties: Map<String, dynamic>.from(
        json['extra_properties'] as Map? ??
            baseline['extra_properties'] as Map? ??
            const {},
      ),
      hitProperties: _decodeHitProperties(json, baseline),
      effectRows: json.containsKey('effect_rows')
          ? [
              for (final value in (json['effect_rows'] as List? ?? const []))
                Map<String, dynamic>.from(value as Map),
            ]
          : null,
      stageEffects: {
        for (final entry in (json['stage_effects'] as Map? ?? const {}).entries)
          '${entry.key}': [
            for (final value in (entry.value as List? ?? const []))
              Map<String, dynamic>.from(value as Map),
          ],
      },
    );
  }
}

Map<String, dynamic> _pageValues(Object? value) {
  if (value is Map && value['values'] is Map) {
    return {
      for (final entry in (value['values'] as Map).entries)
        '${entry.key}': entry.value,
    };
  }
  if (value is Map) {
    return {
      for (final entry in value.entries)
        if (entry.key != 'id' &&
            entry.key != 'values' &&
            entry.key != 'buff' &&
            entry.key != 'variant' &&
            entry.key != 'references' &&
            entry.key != 'owner_weapon' &&
            entry.key != 'action' &&
            entry.key != 'state' &&
            entry.key != 'condition' &&
            entry.key != 'segment_id' &&
            entry.key != 'kind' &&
            entry.key != 'template')
          '${entry.key}': entry.value,
    };
  }
  return const {};
}

Map<String, dynamic> _ruleValues(Object? value) {
  final values = _pageValues(value);
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

Map<String, dynamic> _canonicalHitProperties(
  Map<String, dynamic> weapon,
  Map<String, List<Map<String, dynamic>>> variants,
  Map<String, List<Map<String, dynamic>>> variantsSaved,
  Map<String, List<Map<String, dynamic>>> variantsEdit,
  Map<String, dynamic> pageValues,
  Map<String, dynamic>? canonical,
) {
  final references = <String, List<Map<String, dynamic>>>{};
  final metadata = <String, Map<String, dynamic>>{};
  final add =
      (
        String id,
        String state, {
        String condition = '',
        String segment = '',
        String kind = 'stage',
      }) {
        if (id.trim().isEmpty) return;
        final stage = (weapon['stages'] as List? ?? const []).cast<Map>().where(
          (row) => '${row['state']}' == state,
        );
        final row = stage.isEmpty ? const <String, dynamic>{} : stage.first;
        metadata[id] = {
          ...?metadata[id],
          'id': id,
          if ('${row['action'] ?? ''}'.isNotEmpty) 'action': '${row['action']}',
          'owner_weapon': '${weapon['id']}',
          'state': int.tryParse(state) ?? 0,
          if (condition.isNotEmpty) 'condition': int.tryParse(condition) ?? 0,
          if (segment.isNotEmpty) 'segment_id': segment,
        };
        final ref = <String, dynamic>{
          'weapon': '${weapon['id']}',
          'state': int.tryParse(state) ?? 0,
          if (condition.isNotEmpty) 'condition': int.tryParse(condition) ?? 0,
          if (segment.isNotEmpty) 'segment': segment,
          if (kind.isNotEmpty) 'kind': kind,
        };
        final list = references.putIfAbsent(id, () => []);
        if (!list.any((item) => jsonEncode(item) == jsonEncode(ref)))
          list.add(ref);
      };
  final effectiveVariants = <String, List<Map<String, dynamic>>>{};
  void mergeVariants(Map<String, List<Map<String, dynamic>>> source) {
    for (final entry in source.entries) {
      final rows = effectiveVariants.putIfAbsent(entry.key, () => []);
      for (final branch in entry.value) {
        final condition = '${branch['condition']}';
        rows.removeWhere((row) => '${row['condition']}' == condition);
        if (branch['remove'] != true) rows.add(branch);
      }
    }
  }

  mergeVariants(variants);
  mergeVariants(variantsSaved);
  mergeVariants(variantsEdit);
  for (final stage in (weapon['stages'] as List? ?? const [])) {
    final state = '${(stage as Map)['state']}';
    for (final id in (stage['property_ids'] as List? ?? const [])) {
      add('$id', state);
    }
    for (final hit in (stage['hits'] as List? ?? const [])) {
      final id = '${(hit as Map)['id']}';
      if (!effectiveVariants.values
          .expand((rows) => rows)
          .any(
            (branch) => (branch['segments'] as List? ?? const []).any(
              (segment) => '${(segment as Map)['skillproid']}' == id,
            ),
          )) {
        add(id, state);
      }
    }
  }
  for (final entry in effectiveVariants.entries) {
    for (final branch in entry.value) {
      for (final segment in (branch['segments'] as List? ?? const [])) {
        final row = segment as Map;
        add(
          '${row['skillproid'] ?? ''}',
          entry.key,
          condition: '${branch['condition']}',
          segment: '${row['anm_id'] ?? row['start'] ?? ''}',
          kind: 'variant',
        );
      }
    }
  }
  return {
    for (final id
        in canonical?.keys ?? <String>{...metadata.keys, ...pageValues.keys})
      id: {
        ...metadata[id] ?? {'id': id, 'owner_weapon': '${weapon['id']}'},
        ..._canonicalMetadata(canonical?[id]),
        'values': _deepCopy(pageValues[id] ?? const {}),
        if (references[id]?.isNotEmpty == true) 'references': references[id],
      },
  };
}

dynamic _canonicalMetadataValue(String key, Object? value) {
  if (key != 'buff') return _deepCopy(value);
  if (value is int) return value;
  if (value is num && value.isFinite && value == value.truncate()) {
    return value.toInt();
  }
  return int.tryParse('${value ?? ''}'.trim());
}

Map<String, dynamic> _canonicalMetadata(Object? value) {
  if (value is! Map) return const {};
  final source = value['values'] is Map
      ? {
          for (final entry in value.entries)
            if (entry.key != 'values') '${entry.key}': entry.value,
        }
      : {
          for (final entry in value.entries)
            if ({
              'id',
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
            }.contains('${entry.key}'))
              '${entry.key}': entry.value,
        };
  return {
    for (final entry in source.entries)
      if (entry.key != 'buff' || _canonicalMetadataValue('buff', entry.value) != null)
        '${entry.key}': _canonicalMetadataValue('${entry.key}', entry.value),
  };
}

Map<String, dynamic> _decodeHitProperties(
  Map<String, dynamic> json,
  Map baseline,
) {
  final stageValues = <String, dynamic>{};
  for (final stage
      in ((json['weapon'] as Map?)?['stages'] as List? ?? const [])) {
    for (final hit in ((stage as Map)['hits'] as List? ?? const [])) {
      if (hit is Map) {
        final id = '${hit['id']}';
        if (id.isNotEmpty) stageValues[id] = hit;
      }
    }
  }

  final ruleValues = <String, dynamic>{};
  for (final rule in (json['rules'] as List? ?? const [])) {
    final properties = (rule as Map)['properties'];
    if (properties is Map) {
      for (final entry in properties.entries) {
        ruleValues['${entry.key}'] = entry.value;
      }
    }
  }

  Map<String, dynamic> merged(String id, Object? canonical) {
    final result = <String, dynamic>{
      'id': id,
      ..._canonicalMetadata(stageValues[id]),
      ..._canonicalMetadata(ruleValues[id]),
      ..._canonicalMetadata(canonical),
      'values': {
        ..._pageValues(stageValues[id]),
        ..._pageValues(ruleValues[id]),
        ..._pageValues(canonical),
      },
    };
    return result;
  }

  final rawCanonical = json['hit_properties'];
  if (json.containsKey('hit_properties') && rawCanonical is Map) {
    return {
      for (final entry in rawCanonical.entries)
        '${entry.key}': merged('${entry.key}', entry.value),
    };
  }

  final migrated = <String, dynamic>{};
  final ids = <String>{...stageValues.keys, ...ruleValues.keys};
  if (ids.isEmpty) {
    final legacyRules = baseline['drafts'];
    if (legacyRules is Map) {
      for (final rules in legacyRules.values) {
        for (final rule in (rules as List? ?? const [])) {
          final properties = (rule as Map)['properties'];
          if (properties is Map) {
            for (final entry in properties.entries) {
              ruleValues['${entry.key}'] = entry.value;
              ids.add('${entry.key}');
            }
          }
        }
      }
    }
  }
  for (final id in ids) {
    migrated[id] = merged(id, null);
  }
  return migrated;
}

Map<String, List<Map<String, dynamic>>> _normalizeVariants(
  Map<String, List<Map<String, dynamic>>> variants,
) => {
  for (final entry in variants.entries)
    entry.key: [
      for (final row in entry.value)
        {
          ...Map<String, dynamic>.from(_deepCopy(row) as Map),
          'condition': _variantCondition(row['condition']),
        },
    ],
};

int _variantCondition(Object? value) {
  if (value is int) return value;
  if (value is String && RegExp(r'^[+-]?[0-9]+$').hasMatch(value)) {
    final parsed = int.tryParse(value);
    if (parsed != null) return parsed;
  }
  throw FormatException('动作分支 condition 必须为整数', value);
}

Object? _deepCopy(Object? value) {
  if (value == null || value is String || value is num || value is bool) {
    return value;
  }
  if (value is Set) return value.map(_deepCopy).toList();
  if (value is List) return value.map(_deepCopy).toList();
  if (value is Map) {
    return {
      for (final entry in value.entries) '${entry.key}': _deepCopy(entry.value),
    };
  }
  throw ArgumentError(
    'Workspace value is not JSON-compatible: ${value.runtimeType}',
  );
}
