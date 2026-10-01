import 'package:flutter/material.dart';

import '../../api/models.dart';
import '../../services/app_services.dart';
import '../../services/realtime.dart';
import '../../widgets/common.dart';
import 'staff_dialogs.dart';

/// The counter console: call next, complete, no-show, transfer, priority, pause.
class ConsoleScreen extends StatefulWidget {
  const ConsoleScreen({super.key});

  @override
  State<ConsoleScreen> createState() => _ConsoleScreenState();
}

class _ConsoleScreenState extends State<ConsoleScreen> with LiveRefresh<ConsoleScreen> {
  List<Department> _departments = [];
  int? _deptId;
  QueueView? _view;
  String? _error;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _deptId = services.session.user?.departmentId;
    _init();
    startLive();
  }

  @override
  Duration get pollInterval => const Duration(seconds: 20);

  @override
  bool wantsEvent(QueueEvent e) => _deptId == null || e.affects(_deptId!) || e.action == 'paused' || e.action == 'resumed';

  @override
  Future<void> onLiveRefresh() => _refreshAll();

  Future<void> _init() async {
    await _loadDepartments();
    if (_deptId == null && _departments.isNotEmpty) _deptId = _departments.first.id;
    await _loadQueue();
  }

  Future<void> _refreshAll() async {
    await Future.wait([_loadDepartments(), _loadQueue()]);
  }

  Future<void> _loadDepartments() async {
    try {
      final d = await services.api.departments();
      if (mounted) setState(() => _departments = d);
    } catch (_) {
      // Queue load reports connectivity errors.
    }
  }

  Future<void> _loadQueue() async {
    final id = _deptId;
    if (id == null) return;
    try {
      final v = await services.api.queue(id);
      if (!mounted || id != _deptId) return;
      setState(() {
        _view = v;
        _error = null;
      });
    } catch (e) {
      if (mounted) setState(() => _error = errorText(e));
    }
  }

  Future<void> _run(Future<String?> Function() action) async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      final msg = await action();
      if (msg != null && mounted) showSnack(context, msg);
    } catch (e) {
      if (mounted) showSnack(context, errorText(e), error: true);
    } finally {
      await _loadQueue();
      if (mounted) setState(() => _busy = false);
    }
  }

  // ------------------------------------------------------------------ actions

  void _callNext() => _run(() async {
        final t = await services.api.callNext(_deptId!);
        return 'Calling ${t.tokenNumber} - ${t.visitorName}';
      });

  void _complete(QueueToken t) => _run(() async {
        await services.api.complete(t.id);
        return '${t.tokenNumber} completed';
      });

  Future<void> _noShow(QueueToken t) async {
    final second = t.noShowCount >= 1;
    final ok = await confirmDialog(
      context,
      title: 'Mark ${t.tokenNumber} as no-show?',
      message: second
          ? 'This is the second no-show for this token. It will be cancelled automatically.'
          : 'The token will be moved to the end of the queue. A second no-show cancels it.',
      confirm: second ? 'No-show & cancel' : 'No-show',
      danger: second,
    );
    if (!ok) return;
    _run(() async {
      final r = await services.api.noShow(t.id);
      return r.action == 'cancelled'
          ? '${t.tokenNumber} cancelled after second no-show'
          : '${t.tokenNumber} moved to the end of the queue';
    });
  }

  Future<void> _transfer(QueueToken t) async {
    final res = await showTransferDialog(context, token: t, departments: _departments);
    if (res == null) return;
    _run(() async {
      final moved = await services.api.transfer(t.id, res.departmentId, note: res.note);
      return '${t.tokenNumber} transferred to ${moved.departmentName} as ${moved.tokenNumber}';
    });
  }

  Future<void> _togglePriority(QueueToken t) async {
    if (t.isPriority) {
      _run(() async {
        await services.api.setPriority(t.id, false);
        return 'Priority removed from ${t.tokenNumber}';
      });
      return;
    }
    final reason = await showPriorityReasonDialog(context);
    if (reason == null) return;
    _run(() async {
      await services.api.setPriority(t.id, true, reason: reason);
      return '${t.tokenNumber} marked as priority';
    });
  }

  Future<void> _cancel(QueueToken t) async {
    final reason = await showCancelDialog(context, t);
    if (reason == null) return;
    _run(() async {
      await services.api.staffCancel(t.id, reason: reason);
      return '${t.tokenNumber} cancelled';
    });
  }

  Future<void> _togglePause(Department d) async {
    if (d.isPaused) {
      _run(() async {
        await services.api.setPaused(d.id, false);
        await _loadDepartments();
        return '${d.name} resumed';
      });
      return;
    }
    final reason = await showPauseDialog(context, d);
    if (reason == null) return;
    _run(() async {
      await services.api.setPaused(d.id, true, reason: reason);
      await _loadDepartments();
      return '${d.name} paused - new tokens are blocked';
    });
  }

  Future<void> _issueToken() async {
    final d = _view?.department;
    if (d == null) return;
    final res = await showIssueTokenDialog(context, d);
    if (res == null || !mounted) return;
    showSnack(context, 'Issued ${res.token.tokenNumber} for ${res.token.visitorName}');
    _loadQueue();
  }

  // --------------------------------------------------------------------- UI

  @override
  Widget build(BuildContext context) {
    final view = _view;
    return Scaffold(
      floatingActionButton: view == null
          ? null
          : FloatingActionButton.extended(
              heroTag: 'issue-token',
              onPressed: view.department.isPaused ? null : _issueToken,
              icon: const Icon(Icons.add),
              label: const Text('Issue token'),
            ),
      body: view == null
          ? (_error != null
              ? ErrorRetry(message: _error!, onRetry: _init)
              : const Center(child: CircularProgressIndicator()))
          : RefreshIndicator(
              onRefresh: _refreshAll,
              child: LayoutBuilder(builder: (context, c) {
                final wide = c.maxWidth >= 1000;
                final left = Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
                  _servingCard(view),
                  const SizedBox(height: 12),
                  _callNextButton(view),
                ]);
                final right = _waitingCard(view);
                return ListView(
                  padding: const EdgeInsets.fromLTRB(16, 16, 16, 96),
                  children: [
                    _header(view.department),
                    const SizedBox(height: 12),
                    if (_error != null)
                      Padding(
                        padding: const EdgeInsets.only(bottom: 12),
                        child: Banner2(icon: Icons.cloud_off, color: Colors.red, text: _error!),
                      ),
                    if (wide)
                      Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
                        Expanded(flex: 5, child: left),
                        const SizedBox(width: 16),
                        Expanded(flex: 6, child: right),
                      ])
                    else ...[
                      left,
                      const SizedBox(height: 16),
                      right,
                    ],
                    const SizedBox(height: 16),
                    _recentCard(view),
                  ],
                );
              }),
            ),
    );
  }

  Widget _header(Department d) {
    final theme = Theme.of(context);
    final isAdmin = services.session.isAdmin;
    return Card(
      elevation: 0,
      color: d.isPaused ? Colors.orange.withAlpha(30) : theme.colorScheme.primaryContainer,
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Row(children: [
            CircleAvatar(child: Icon(departmentIcon(d.code))),
            const SizedBox(width: 12),
            Expanded(
              child: isAdmin && _departments.isNotEmpty
                  ? DropdownButtonHideUnderline(
                      child: DropdownButton<int>(
                        value: _departments.any((e) => e.id == _deptId) ? _deptId : null,
                        isExpanded: true,
                        style: theme.textTheme.titleLarge,
                        items: _departments
                            .map((e) => DropdownMenuItem(value: e.id, child: Text('${e.name} (${e.code})')))
                            .toList(),
                        onChanged: (v) {
                          if (v == null || v == _deptId) return;
                          setState(() {
                            _deptId = v;
                            _view = null;
                          });
                          _loadQueue();
                        },
                      ),
                    )
                  : Text('${d.name} (${d.code})', style: theme.textTheme.titleLarge),
            ),
            Column(crossAxisAlignment: CrossAxisAlignment.end, children: [
              Text(d.isPaused ? 'Paused' : 'Open', style: theme.textTheme.labelMedium),
              Switch(
                value: !d.isPaused,
                onChanged: _busy ? null : (_) => _togglePause(d),
              ),
            ]),
          ]),
          const SizedBox(height: 8),
          Wrap(spacing: 8, runSpacing: 8, children: [
            Pill(label: '${d.waitingCount} waiting', color: Colors.blue, icon: Icons.people_alt_outlined),
            Pill(
              label: 'Avg service ${formatMinutes(d.effectiveServiceMinutes)}',
              color: Colors.teal,
              icon: Icons.timer_outlined,
            ),
            Pill(
              label: 'New token wait ${formatEta(d.estimatedWaitMinutes)}',
              color: Colors.indigo,
              icon: Icons.hourglass_bottom_rounded,
            ),
          ]),
          if (d.isPaused) ...[
            const SizedBox(height: 10),
            Text(
              'Paused${d.pauseReason.isEmpty ? '' : ': ${d.pauseReason}'}. New tokens, transfers in and Call Next are blocked. '
              'Waiting visitors keep their place.',
              style: TextStyle(color: Colors.orange.shade900),
            ),
          ],
        ]),
      ),
    );
  }

  Widget _servingCard(QueueView view) {
    final t = view.serving;
    final theme = Theme.of(context);
    if (t == null) {
      return Card(
        elevation: 0,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(16),
          side: BorderSide(color: theme.colorScheme.outlineVariant),
        ),
        child: const Padding(
          padding: EdgeInsets.all(28),
          child: Column(children: [
            Icon(Icons.event_seat_outlined, size: 40),
            SizedBox(height: 8),
            Text('No one at the counter'),
            SizedBox(height: 4),
            Text('Press "Call next" to serve the next visitor.', textAlign: TextAlign.center),
          ]),
        ),
      );
    }
    return Card(
      elevation: 0,
      color: Colors.green.withAlpha(28),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(16),
        side: BorderSide(color: Colors.green.withAlpha(120), width: 1.5),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
          Text('NOW SERVING', style: theme.textTheme.labelLarge?.copyWith(color: Colors.green.shade800)),
          const SizedBox(height: 4),
          Row(children: [
            Text(t.tokenNumber, style: theme.textTheme.displaySmall?.copyWith(fontWeight: FontWeight.w800)),
            const SizedBox(width: 12),
            Expanded(
              child: Wrap(spacing: 6, runSpacing: 6, children: [
                if (t.isPriority) PriorityPill(reason: t.priorityReason),
                if (t.noShowCount > 0) Pill(label: 'No-show ×${t.noShowCount}', color: Colors.deepOrange),
                if (t.transferredFrom.isNotEmpty) Pill(label: 'From ${t.transferredFrom}', color: Colors.blueGrey),
              ]),
            ),
          ]),
          const SizedBox(height: 6),
          Text(t.visitorName, style: theme.textTheme.titleMedium),
          if (t.visitorPhone.isNotEmpty) Text(t.visitorPhone),
          Text('Called at ${formatClock(t.calledAt)} · at counter ${formatElapsed(t.calledAt)}',
              style: theme.textTheme.bodySmall),
          const SizedBox(height: 12),
          Wrap(spacing: 8, runSpacing: 8, children: [
            FilledButton.icon(
              style: FilledButton.styleFrom(backgroundColor: Colors.green.shade700),
              onPressed: _busy ? null : () => _complete(t),
              icon: const Icon(Icons.check_rounded),
              label: const Text('Complete'),
            ),
            OutlinedButton.icon(
              style: OutlinedButton.styleFrom(foregroundColor: Colors.deepOrange),
              onPressed: _busy ? null : () => _noShow(t),
              icon: const Icon(Icons.person_off_outlined),
              label: const Text('No show'),
            ),
            OutlinedButton.icon(
              onPressed: _busy ? null : () => _transfer(t),
              icon: const Icon(Icons.swap_horiz_rounded),
              label: const Text('Transfer'),
            ),
            TextButton.icon(
              style: TextButton.styleFrom(foregroundColor: theme.colorScheme.error),
              onPressed: _busy ? null : () => _cancel(t),
              icon: const Icon(Icons.close_rounded),
              label: const Text('Cancel'),
            ),
          ]),
        ]),
      ),
    );
  }

  Widget _callNextButton(QueueView view) {
    final d = view.department;
    final next = view.waiting.isEmpty ? null : view.waiting.first;
    String hint;
    if (d.isPaused) {
      hint = 'Department is paused';
    } else if (view.serving != null) {
      hint = 'Complete or mark no-show for ${view.serving!.tokenNumber} first';
    } else if (next == null) {
      hint = 'Queue is empty';
    } else {
      hint = 'Next: ${next.tokenNumber}${next.inPriorityLane ? ' (priority)' : ''} - ${next.visitorName}';
    }
    final enabled = !_busy && !d.isPaused && view.serving == null && next != null;
    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      FilledButton.icon(
        style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(60), textStyle: const TextStyle(fontSize: 18)),
        onPressed: enabled ? _callNext : null,
        icon: _busy
            ? const SizedBox(width: 22, height: 22, child: CircularProgressIndicator(strokeWidth: 2))
            : const Icon(Icons.campaign_rounded),
        label: const Text('Call next token'),
      ),
      const SizedBox(height: 6),
      Text(hint, textAlign: TextAlign.center, style: Theme.of(context).textTheme.bodySmall),
    ]);
  }

  Widget _waitingCard(QueueView view) {
    final theme = Theme.of(context);
    return Card(
      elevation: 0,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(16),
        side: BorderSide(color: theme.colorScheme.outlineVariant),
      ),
      child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 14, 16, 6),
          child: Row(children: [
            Text('Waiting queue', style: theme.textTheme.titleMedium),
            const Spacer(),
            Text('${view.waiting.length} in line', style: theme.textTheme.labelLarge),
          ]),
        ),
        if (view.waiting.isEmpty)
          const Padding(
            padding: EdgeInsets.all(24),
            child: Center(child: Text('Nobody is waiting')),
          ),
        ...view.waiting.map((t) => _waitingTile(t)),
        const SizedBox(height: 8),
      ]),
    );
  }

  Widget _waitingTile(QueueToken t) {
    final theme = Theme.of(context);
    return ListTile(
      leading: CircleAvatar(
        backgroundColor: t.inPriorityLane ? Colors.purple : theme.colorScheme.secondaryContainer,
        foregroundColor: t.inPriorityLane ? Colors.white : theme.colorScheme.onSecondaryContainer,
        child: Text('${t.position ?? ''}'),
      ),
      title: Wrap(spacing: 6, runSpacing: 4, crossAxisAlignment: WrapCrossAlignment.center, children: [
        Text(t.tokenNumber, style: const TextStyle(fontWeight: FontWeight.bold)),
        if (t.isPriority) PriorityPill(reason: t.priorityReason),
        if (t.noShowCount > 0) Pill(label: 'No-show ×${t.noShowCount}', color: Colors.deepOrange),
        if (t.transferredFrom.isNotEmpty) Pill(label: 'From ${t.transferredFrom}', color: Colors.blueGrey),
      ]),
      subtitle: Text(
        '${t.visitorName}${t.visitorPhone.isEmpty ? '' : ' · ${t.visitorPhone}'}\n'
        'Waiting ${formatElapsed(t.enqueuedAt)} · ETA ${formatEta(t.estimatedWaitMinutes)}',
      ),
      isThreeLine: true,
      trailing: PopupMenuButton<String>(
        enabled: !_busy,
        onSelected: (v) {
          switch (v) {
            case 'priority':
              _togglePriority(t);
              break;
            case 'transfer':
              _transfer(t);
              break;
            case 'cancel':
              _cancel(t);
              break;
          }
        },
        itemBuilder: (_) => [
          PopupMenuItem(
            value: 'priority',
            child: ListTile(
              leading: Icon(t.isPriority ? Icons.star_border_rounded : Icons.star_rounded, color: Colors.purple),
              title: Text(t.isPriority ? 'Remove priority' : 'Mark as priority'),
            ),
          ),
          const PopupMenuItem(
            value: 'transfer',
            child: ListTile(leading: Icon(Icons.swap_horiz_rounded), title: Text('Transfer')),
          ),
          PopupMenuItem(
            value: 'cancel',
            child: ListTile(
              leading: Icon(Icons.close_rounded, color: theme.colorScheme.error),
              title: const Text('Cancel token'),
            ),
          ),
        ],
      ),
    );
  }

  Widget _recentCard(QueueView view) {
    if (view.recent.isEmpty) return const SizedBox.shrink();
    return Card(
      elevation: 0,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(16),
        side: BorderSide(color: Theme.of(context).colorScheme.outlineVariant),
      ),
      child: ExpansionTile(
        shape: const Border(),
        title: Text('Recently closed today (${view.recent.length})'),
        children: view.recent
            .map((t) => ListTile(
                  dense: true,
                  title: Text('${t.tokenNumber} · ${t.visitorName}'),
                  subtitle: t.closeReason.isEmpty ? null : Text(t.closeReason),
                  trailing: Row(mainAxisSize: MainAxisSize.min, children: [
                    Text(formatClock(t.closedAt)),
                    const SizedBox(width: 8),
                    StatusChip(t.status),
                  ]),
                ))
            .toList(),
      ),
    );
  }
}
