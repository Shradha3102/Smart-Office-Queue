import 'package:flutter/material.dart';

import '../api/models.dart';
import '../services/app_services.dart';
import '../widgets/common.dart';
import 'display_board_screen.dart';
import 'staff/staff_gate.dart';
import 'visitor/my_tokens_screen.dart';
import 'visitor/take_token_screen.dart';

class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> with LiveRefresh<HomeScreen> {
  List<Department>? _departments;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
    startLive();
  }

  @override
  Future<void> onLiveRefresh() => _load();

  Future<void> _load() async {
    try {
      final d = await services.api.departments();
      if (!mounted) return;
      setState(() {
        _departments = d;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = errorText(e));
    }
  }

  Future<void> _editServer() async {
    final ctrl = TextEditingController(text: services.serverUrl);
    final url = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Server address'),
        content: TextField(
          controller: ctrl,
          autofocus: true,
          keyboardType: TextInputType.url,
          decoration: const InputDecoration(labelText: 'API base URL', hintText: 'http://192.168.1.10:8080'),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
          FilledButton(onPressed: () => Navigator.pop(ctx, ctrl.text), child: const Text('Save')),
        ],
      ),
    );
    if (url == null || url.trim().isEmpty) return;
    final parsed = Uri.tryParse(url.trim());
    if (parsed == null || !(parsed.scheme == 'http' || parsed.scheme == 'https') || parsed.host.isEmpty) {
      if (mounted) showSnack(context, 'Please enter a valid http(s) URL', error: true);
      return;
    }
    await services.setServerUrl(url);
    final ok = await services.api.health();
    if (!mounted) return;
    showSnack(context, ok ? 'Connected to server' : 'Saved, but the server is not reachable', error: !ok);
    _load();
  }

  void _open(Widget page) =>
      Navigator.of(context).push(MaterialPageRoute(builder: (_) => page)).then((_) => _load());

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(
        title: const Text('Smart Office Queue'),
        actions: [
          const ConnectionDot(),
          IconButton(tooltip: 'Server settings', onPressed: _editServer, icon: const Icon(Icons.settings_outlined)),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: _load,
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            MaxWidth(
              width: 960,
              child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
                Text('Welcome! How can we help you today?', style: theme.textTheme.headlineSmall),
                const SizedBox(height: 4),
                Text('Take a token, relax, and track your turn live.',
                    style: theme.textTheme.bodyMedium?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
                const SizedBox(height: 16),
                LayoutBuilder(builder: (context, c) {
                  final wide = c.maxWidth > 640;
                  final tiles = <Widget>[
                    _ActionTile(
                      icon: Icons.confirmation_number_rounded,
                      title: 'Get a token',
                      subtitle: 'Choose a service and join the queue',
                      color: theme.colorScheme.primary,
                      onTap: () => _open(const TakeTokenScreen()),
                    ),
                    ListenableBuilder(
                      listenable: services.myTokens,
                      builder: (context, _) => _ActionTile(
                        icon: Icons.track_changes_rounded,
                        title: 'My tokens',
                        subtitle: services.myTokens.tokens.isEmpty
                            ? 'Track your place in the queue'
                            : '${services.myTokens.tokens.length} token(s) on this device',
                        color: Colors.teal,
                        onTap: () => _open(const MyTokensScreen()),
                      ),
                    ),
                  ];
                  return wide
                      ? Row(children: [
                          Expanded(child: tiles[0]),
                          const SizedBox(width: 12),
                          Expanded(child: tiles[1]),
                        ])
                      : Column(children: [tiles[0], const SizedBox(height: 12), tiles[1]]);
                }),
                const SizedBox(height: 8),
                Wrap(spacing: 8, runSpacing: 8, children: [
                  OutlinedButton.icon(
                    onPressed: () => _open(const StaffGate()),
                    icon: const Icon(Icons.badge_outlined),
                    label: const Text('Staff / Admin login'),
                  ),
                  OutlinedButton.icon(
                    onPressed: () => _open(const DisplayBoardScreen()),
                    icon: const Icon(Icons.tv_rounded),
                    label: const Text('Display board'),
                  ),
                ]),
                const SizedBox(height: 24),
                Text('Live queue status', style: theme.textTheme.titleMedium),
                const SizedBox(height: 8),
                if (_error != null && _departments == null)
                  ErrorRetry(message: _error!, onRetry: _load)
                else if (_departments == null)
                  const Padding(padding: EdgeInsets.all(32), child: Center(child: CircularProgressIndicator()))
                else
                  ..._departments!.map((d) => _DepartmentStatusCard(d)),
              ]),
            ),
          ],
        ),
      ),
    );
  }
}

class _ActionTile extends StatelessWidget {
  const _ActionTile({
    required this.icon,
    required this.title,
    required this.subtitle,
    required this.color,
    required this.onTap,
  });

  final IconData icon;
  final String title;
  final String subtitle;
  final Color color;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Card(
      color: color.withAlpha(22),
      elevation: 0,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(20), side: BorderSide(color: color.withAlpha(80))),
      child: InkWell(
        borderRadius: BorderRadius.circular(20),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.all(20),
          child: Row(children: [
            CircleAvatar(radius: 28, backgroundColor: color, child: Icon(icon, color: Colors.white, size: 28)),
            const SizedBox(width: 16),
            Expanded(
              child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                Text(title, style: Theme.of(context).textTheme.titleLarge),
                const SizedBox(height: 2),
                Text(subtitle),
              ]),
            ),
            const Icon(Icons.chevron_right_rounded),
          ]),
        ),
      ),
    );
  }
}

class _DepartmentStatusCard extends StatelessWidget {
  const _DepartmentStatusCard(this.d);
  final Department d;

  @override
  Widget build(BuildContext context) {
    return Card(
      elevation: 0,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(14),
        side: BorderSide(color: Theme.of(context).colorScheme.outlineVariant),
      ),
      child: ListTile(
        leading: CircleAvatar(child: Icon(departmentIcon(d.code))),
        title: Row(children: [
          Flexible(child: Text(d.name, overflow: TextOverflow.ellipsis)),
          const SizedBox(width: 8),
          d.isPaused
              ? const Pill(label: 'Paused', color: Colors.orange, icon: Icons.pause_circle_filled_rounded)
              : const Pill(label: 'Open', color: Colors.green),
        ]),
        subtitle: Text(d.isPaused
            ? (d.pauseReason.isEmpty ? 'Not accepting new tokens right now' : d.pauseReason)
            : '${d.waitingCount} waiting · est. wait ${formatEta(d.estimatedWaitMinutes)}'),
        trailing: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            Text('Serving', style: Theme.of(context).textTheme.labelSmall),
            Text(d.nowServing ?? '-', style: Theme.of(context).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.bold)),
          ],
        ),
      ),
    );
  }
}
