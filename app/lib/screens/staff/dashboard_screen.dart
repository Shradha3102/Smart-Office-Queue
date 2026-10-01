import 'package:flutter/material.dart';

import '../../api/models.dart';
import '../../services/app_services.dart';
import '../../widgets/common.dart';

/// Today's live statistics: waiting, serving, completed, no-shows, average wait.
class DashboardScreen extends StatefulWidget {
  const DashboardScreen({super.key});

  @override
  State<DashboardScreen> createState() => _DashboardScreenState();
}

class _DashboardScreenState extends State<DashboardScreen> with LiveRefresh<DashboardScreen> {
  DashboardData? _data;
  String? _error;
  int? _filter; // admin department filter (null = all)
  List<Department> _allDepartments = [];

  @override
  void initState() {
    super.initState();
    _load();
    startLive();
    if (services.session.isAdmin) {
      services.api.departments().then((d) {
        if (mounted) setState(() => _allDepartments = d);
      }).catchError((_) {});
    }
  }

  @override
  Duration get pollInterval => const Duration(seconds: 30);

  @override
  Future<void> onLiveRefresh() => _load();

  Future<void> _load() async {
    try {
      final d = await services.api.dashboard(departmentId: _filter);
      if (!mounted) return;
      setState(() {
        _data = d;
        _error = null;
      });
    } catch (e) {
      if (mounted) setState(() => _error = errorText(e));
    }
  }

  Future<void> _editServiceTime(DeptStats s) async {
    final ctrl = TextEditingController();
    final minutes = await showDialog<int>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('${s.name}: default service time'),
        content: TextField(
          controller: ctrl,
          autofocus: true,
          keyboardType: TextInputType.number,
          decoration: const InputDecoration(
            labelText: 'Minutes per visitor (1-240)',
            helperText: 'Used for ETAs until enough real service data is available.',
          ),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
          FilledButton(onPressed: () => Navigator.pop(ctx, int.tryParse(ctrl.text.trim())), child: const Text('Save')),
        ],
      ),
    );
    if (minutes == null) return;
    try {
      await services.api.updateDepartment(s.departmentId, avgServiceMinutes: minutes);
      if (mounted) showSnack(context, 'Updated ${s.name}');
      _load();
    } catch (e) {
      if (mounted) showSnack(context, errorText(e), error: true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final data = _data;
    if (data == null) {
      return _error != null
          ? ErrorRetry(message: _error!, onRetry: _load)
          : const Center(child: CircularProgressIndicator());
    }
    final isAdmin = services.session.isAdmin;
    final t = data.totals;
    final theme = Theme.of(context);

    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Row(children: [
            Expanded(
              child: Text('Today · ${data.date}', style: theme.textTheme.titleMedium),
            ),
            if (isAdmin)
              DropdownButton<int?>(
                value: _allDepartments.any((d) => d.id == _filter) ? _filter : null,
                hint: const Text('All departments'),
                items: [
                  const DropdownMenuItem<int?>(value: null, child: Text('All departments')),
                  ..._allDepartments.map((d) => DropdownMenuItem<int?>(value: d.id, child: Text(d.name))),
                ],
                onChanged: (v) {
                  setState(() => _filter = v);
                  _load();
                },
              ),
          ]),
          if (_error != null)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Banner2(icon: Icons.cloud_off, color: Colors.red, text: _error!),
            ),
          const SizedBox(height: 12),
          LayoutBuilder(builder: (context, c) {
            final cols = c.maxWidth > 1100 ? 4 : (c.maxWidth > 560 ? 3 : 2);
            final cards = <Widget>[
              _Kpi('Waiting', '${t.waiting}', Icons.hourglass_top_rounded, Colors.blue,
                  sub: t.priorityWaiting > 0 ? '${t.priorityWaiting} priority' : null),
              _Kpi('Currently serving', '${t.serving}', Icons.campaign_rounded, Colors.green,
                  sub: _servingList(data)),
              _Kpi('Completed', '${t.completed}', Icons.verified_rounded, Colors.teal),
              _Kpi('No-shows', '${t.noShows}', Icons.person_off_rounded, Colors.deepOrange,
                  sub: t.autoCancelledNoShows > 0 ? '${t.autoCancelledNoShows} auto-cancelled' : null),
              _Kpi('Avg waiting time', formatMinutes(t.avgWaitMinutes), Icons.timer_outlined, Colors.indigo,
                  sub: '${t.called} calls today'),
              _Kpi('Avg service time', formatMinutes(t.avgServiceMinutes), Icons.speed_rounded, Colors.purple),
              _Kpi('Tokens issued', '${t.issued}', Icons.confirmation_number_outlined, Colors.blueGrey),
              _Kpi('Cancelled', '${t.cancelled}', Icons.cancel_outlined, Colors.grey,
                  sub: t.transferredOut > 0 ? '${t.transferredOut} transferred' : null),
            ];
            return GridView.count(
              crossAxisCount: cols,
              shrinkWrap: true,
              physics: const NeverScrollableScrollPhysics(),
              mainAxisSpacing: 10,
              crossAxisSpacing: 10,
              childAspectRatio: 1.7,
              children: cards,
            );
          }),
          const SizedBox(height: 20),
          Text('By department', style: theme.textTheme.titleMedium),
          const SizedBox(height: 8),
          Card(
            elevation: 0,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(16),
              side: BorderSide(color: theme.colorScheme.outlineVariant),
            ),
            child: SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              child: DataTable(
                columnSpacing: 24,
                columns: [
                  const DataColumn(label: Text('Department')),
                  const DataColumn(label: Text('Status')),
                  const DataColumn(label: Text('Waiting'), numeric: true),
                  const DataColumn(label: Text('Serving')),
                  const DataColumn(label: Text('Completed'), numeric: true),
                  const DataColumn(label: Text('No-shows'), numeric: true),
                  const DataColumn(label: Text('Cancelled'), numeric: true),
                  const DataColumn(label: Text('Avg wait'), numeric: true),
                  const DataColumn(label: Text('Avg service'), numeric: true),
                  const DataColumn(label: Text('Longest wait'), numeric: true),
                  if (isAdmin) const DataColumn(label: Text('')),
                ],
                rows: data.departments
                    .map((s) => DataRow(cells: [
                          DataCell(Row(mainAxisSize: MainAxisSize.min, children: [
                            Icon(departmentIcon(s.code), size: 18),
                            const SizedBox(width: 8),
                            Text(s.name),
                          ])),
                          DataCell(s.isPaused
                              ? const Pill(label: 'Paused', color: Colors.orange)
                              : const Pill(label: 'Open', color: Colors.green)),
                          DataCell(Text('${s.waiting}')),
                          DataCell(Text(s.nowServing ?? '-', style: const TextStyle(fontWeight: FontWeight.bold))),
                          DataCell(Text('${s.completed}')),
                          DataCell(Text('${s.noShows}')),
                          DataCell(Text('${s.cancelled}')),
                          DataCell(Text(formatMinutes(s.avgWaitMinutes))),
                          DataCell(Text(formatMinutes(s.avgServiceMinutes))),
                          DataCell(Text(formatMinutes(s.longestWaitingMinutes))),
                          if (isAdmin)
                            DataCell(IconButton(
                              tooltip: 'Default service time',
                              icon: const Icon(Icons.tune_rounded),
                              onPressed: () => _editServiceTime(s),
                            )),
                        ]))
                    .toList(),
              ),
            ),
          ),
          const SizedBox(height: 8),
          Text(
            'Average waiting time = time from joining a queue until being called. '
            'No-shows count every missed call; a token is auto-cancelled on its second no-show.',
            style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.onSurfaceVariant),
          ),
        ],
      ),
    );
  }

  String? _servingList(DashboardData d) {
    final list = d.departments.where((s) => s.nowServing != null).map((s) => s.nowServing!).toList();
    return list.isEmpty ? null : list.join(', ');
  }
}

class _Kpi extends StatelessWidget {
  const _Kpi(this.label, this.value, this.icon, this.color, {this.sub});
  final String label;
  final String value;
  final IconData icon;
  final Color color;
  final String? sub;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      elevation: 0,
      margin: EdgeInsets.zero,
      color: color.withAlpha(24),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16), side: BorderSide(color: color.withAlpha(70))),
      child: Padding(
        padding: const EdgeInsets.all(14),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, mainAxisAlignment: MainAxisAlignment.spaceBetween, children: [
          Row(children: [
            Icon(icon, color: color, size: 20),
            const SizedBox(width: 6),
            Expanded(child: Text(label, style: theme.textTheme.labelLarge, overflow: TextOverflow.ellipsis)),
          ]),
          FittedBox(
            fit: BoxFit.scaleDown,
            alignment: Alignment.centerLeft,
            child: Text(value, style: theme.textTheme.headlineMedium?.copyWith(fontWeight: FontWeight.bold)),
          ),
          Text(sub ?? '', style: theme.textTheme.bodySmall, maxLines: 1, overflow: TextOverflow.ellipsis),
        ]),
      ),
    );
  }
}
