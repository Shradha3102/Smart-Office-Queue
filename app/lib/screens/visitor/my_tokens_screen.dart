import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../api/models.dart';
import '../../services/app_services.dart';
import '../../widgets/common.dart';
import 'take_token_screen.dart';
import 'token_tracking_screen.dart';

/// Tokens generated on this device during the last 24 hours.
class MyTokensScreen extends StatefulWidget {
  const MyTokensScreen({super.key});

  @override
  State<MyTokensScreen> createState() => _MyTokensScreenState();
}

class _MyTokensScreenState extends State<MyTokensScreen> with LiveRefresh<MyTokensScreen> {
  final Map<String, TokenStatus> _statuses = {};
  final Set<String> _missing = {};
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
    startLive();
  }

  @override
  Future<void> onLiveRefresh() => _load();

  Future<void> _load() async {
    final saved = services.myTokens.tokens;
    await Future.wait(saved.map((s) async {
      try {
        _statuses[s.id] = await services.api.tokenStatus(s.id);
        _missing.remove(s.id);
      } on ApiException catch (e) {
        if (e.code == 'token_not_found') _missing.add(s.id);
      }
    }));
    for (final id in _missing.toList()) {
      await services.myTokens.remove(id);
    }
    if (mounted) setState(() => _loading = false);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('My tokens'), actions: const [ConnectionDot()]),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: () => Navigator.of(context)
            .push(MaterialPageRoute(builder: (_) => const TakeTokenScreen()))
            .then((_) => _load()),
        icon: const Icon(Icons.add),
        label: const Text('New token'),
      ),
      body: ListenableBuilder(
        listenable: services.myTokens,
        builder: (context, _) {
          final saved = services.myTokens.tokens;
          if (saved.isEmpty) {
            return const Center(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: Text('You have no tokens on this device yet.', textAlign: TextAlign.center),
              ),
            );
          }
          if (_loading && _statuses.isEmpty) return const Center(child: CircularProgressIndicator());
          return RefreshIndicator(
            onRefresh: _load,
            child: ListView.separated(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 96),
              itemCount: saved.length,
              separatorBuilder: (_, __) => const SizedBox(height: 8),
              itemBuilder: (context, i) {
                final s = saved[i];
                final st = _statuses[s.id];
                final t = st?.token;
                return MaxWidth(
                  child: Card(
                    elevation: 0,
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(14),
                      side: BorderSide(color: Theme.of(context).colorScheme.outlineVariant),
                    ),
                    child: ListTile(
                      leading: CircleAvatar(child: Icon(departmentIcon(st?.departmentCode ?? ''))),
                      title: Text(t?.tokenNumber ?? s.number,
                          style: const TextStyle(fontWeight: FontWeight.bold, fontSize: 18)),
                      subtitle: Text(t == null
                          ? 'Loading…'
                          : t.isWaiting
                              ? '${st!.departmentName} · position #${t.position} · ${formatEta(t.estimatedWaitMinutes)}'
                              : st!.departmentName),
                      trailing: t == null ? null : StatusChip(t.status),
                      onTap: () => Navigator.of(context)
                          .push(MaterialPageRoute(builder: (_) => TokenTrackingScreen(tokenId: s.id)))
                          .then((_) => _load()),
                      onLongPress: t != null && !t.isActive ? () => services.myTokens.remove(s.id) : null,
                    ),
                  ),
                );
              },
            ),
          );
        },
      ),
    );
  }
}
