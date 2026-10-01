import 'dart:async';

import 'package:flutter/material.dart';

import '../api/models.dart';
import '../services/app_services.dart';
import '../widgets/common.dart';

/// Full-screen "Now serving" board for a lobby TV.
class DisplayBoardScreen extends StatefulWidget {
  const DisplayBoardScreen({super.key});

  @override
  State<DisplayBoardScreen> createState() => _DisplayBoardScreenState();
}

class _DisplayBoardScreenState extends State<DisplayBoardScreen> with LiveRefresh<DisplayBoardScreen> {
  List<Department> _departments = [];
  final Map<int, String?> _lastServing = {};
  final Set<int> _flash = {};
  String? _error;
  Timer? _clock;

  @override
  void initState() {
    super.initState();
    _load();
    startLive();
    _clock = Timer.periodic(const Duration(seconds: 30), (_) {
      if (mounted) setState(() {});
    });
  }

  @override
  void dispose() {
    _clock?.cancel();
    super.dispose();
  }

  @override
  Future<void> onLiveRefresh() => _load();

  Future<void> _load() async {
    try {
      final d = await services.api.departments();
      if (!mounted) return;
      final changed = <int>{};
      for (final dep in d) {
        if (_lastServing.containsKey(dep.id) && _lastServing[dep.id] != dep.nowServing && dep.nowServing != null) {
          changed.add(dep.id);
        }
        _lastServing[dep.id] = dep.nowServing;
      }
      setState(() {
        _departments = d;
        _error = null;
        _flash.addAll(changed);
      });
      if (changed.isNotEmpty) {
        Timer(const Duration(seconds: 4), () {
          if (mounted) setState(() => _flash.removeAll(changed));
        });
      }
    } catch (e) {
      if (mounted) setState(() => _error = errorText(e));
    }
  }

  @override
  Widget build(BuildContext context) {
    final now = DateTime.now();
    return Scaffold(
      backgroundColor: const Color(0xFF0E1630),
      appBar: AppBar(
        backgroundColor: const Color(0xFF0E1630),
        foregroundColor: Colors.white,
        title: const Text('Now Serving'),
        actions: [
          Center(child: Text(formatClock(now), style: const TextStyle(fontSize: 20, color: Colors.white))),
          const ConnectionDot(),
        ],
      ),
      body: _error != null && _departments.isEmpty
          ? ErrorRetry(message: _error!, onRetry: _load)
          : LayoutBuilder(builder: (context, c) {
              final cols = c.maxWidth > 1100 ? 4 : (c.maxWidth > 700 ? 2 : 1);
              return GridView.count(
                padding: const EdgeInsets.all(16),
                crossAxisCount: cols,
                mainAxisSpacing: 16,
                crossAxisSpacing: 16,
                childAspectRatio: cols == 1 ? 1.8 : 1.1,
                children: _departments.map((d) => _BoardTile(d, flash: _flash.contains(d.id))).toList(),
              );
            }),
    );
  }
}

class _BoardTile extends StatelessWidget {
  const _BoardTile(this.d, {required this.flash});
  final Department d;
  final bool flash;

  @override
  Widget build(BuildContext context) {
    return AnimatedContainer(
      duration: const Duration(milliseconds: 500),
      decoration: BoxDecoration(
        color: flash ? const Color(0xFF1B8E4B) : const Color(0xFF18234A),
        borderRadius: BorderRadius.circular(24),
        border: Border.all(color: d.isPaused ? Colors.orange : const Color(0xFF2C3A70), width: 2),
      ),
      padding: const EdgeInsets.all(20),
      child: Column(mainAxisAlignment: MainAxisAlignment.center, children: [
        Row(mainAxisAlignment: MainAxisAlignment.center, children: [
          Icon(departmentIcon(d.code), color: Colors.white70),
          const SizedBox(width: 8),
          Flexible(
            child: Text(d.name,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(color: Colors.white70, fontSize: 22, fontWeight: FontWeight.w500)),
          ),
        ]),
        const SizedBox(height: 12),
        Expanded(
          child: FittedBox(
            child: Text(
              d.nowServing ?? '-',
              style: const TextStyle(color: Colors.white, fontSize: 96, fontWeight: FontWeight.w800, letterSpacing: 3),
            ),
          ),
        ),
        const SizedBox(height: 8),
        Text(
          d.isPaused ? 'PAUSED${d.pauseReason.isEmpty ? '' : ' · ${d.pauseReason}'}' : '${d.waitingCount} waiting',
          style: TextStyle(color: d.isPaused ? Colors.orange : Colors.white60, fontSize: 18),
        ),
      ]),
    );
  }
}
