import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../api/models.dart';
import '../../services/app_services.dart';
import '../../services/realtime.dart';
import '../../widgets/common.dart';
import 'token_tracking_screen.dart';

/// Visitor flow: pick a service, enter details, receive a token.
class TakeTokenScreen extends StatefulWidget {
  const TakeTokenScreen({super.key});

  @override
  State<TakeTokenScreen> createState() => _TakeTokenScreenState();
}

class _TakeTokenScreenState extends State<TakeTokenScreen> with LiveRefresh<TakeTokenScreen> {
  final _formKey = GlobalKey<FormState>();
  final _name = TextEditingController();
  final _phone = TextEditingController();

  List<Department>? _departments;
  String? _loadError;
  int? _selectedId;
  bool _priority = false;
  String? _priorityReason;
  bool _submitting = false;

  @override
  void initState() {
    super.initState();
    _load();
    startLive();
  }

  @override
  void dispose() {
    _name.dispose();
    _phone.dispose();
    super.dispose();
  }

  @override
  Future<void> onLiveRefresh() => _load();

  @override
  bool wantsEvent(QueueEvent event) => true;

  Department? get _selected {
    for (final d in _departments ?? const <Department>[]) {
      if (d.id == _selectedId) return d;
    }
    return null;
  }

  Future<void> _load() async {
    try {
      final d = await services.api.departments();
      if (!mounted) return;
      setState(() {
        _departments = d;
        _loadError = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _loadError = errorText(e));
    }
  }

  Future<void> _submit() async {
    final dept = _selected;
    if (dept == null) {
      showSnack(context, 'Please choose a service first', error: true);
      return;
    }
    if (dept.isPaused) {
      showSnack(context, '${dept.name} is paused right now. Please choose another service or try later.', error: true);
      return;
    }
    if (!(_formKey.currentState?.validate() ?? false)) return;

    setState(() => _submitting = true);
    try {
      final res = await services.api.createToken(
        departmentId: dept.id,
        visitorName: _name.text.trim(),
        visitorPhone: _phone.text.trim(),
        isPriority: _priority,
        priorityReason: _priority ? (_priorityReason ?? '') : '',
      );
      await services.myTokens.add(SavedToken(
        id: res.token.id,
        secret: res.accessSecret ?? '',
        number: res.token.tokenNumber,
        createdAt: DateTime.now(),
      ));
      if (!mounted) return;
      Navigator.of(context).pushReplacement(MaterialPageRoute(
        builder: (_) => TokenTrackingScreen(tokenId: res.token.id, justCreated: true),
      ));
    } on ApiException catch (e) {
      if (!mounted) return;
      showSnack(context, e.message, error: true);
      if (e.code == 'department_paused') _load();
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(title: const Text('Get a token'), actions: const [ConnectionDot()]),
      body: _departments == null
          ? (_loadError != null
              ? ErrorRetry(message: _loadError!, onRetry: _load)
              : const Center(child: CircularProgressIndicator()))
          : ListView(
              padding: const EdgeInsets.all(16),
              children: [
                MaxWidth(
                  child: Form(
                    key: _formKey,
                    child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
                      Text('1. Choose a service', style: theme.textTheme.titleMedium),
                      const SizedBox(height: 8),
                      GridView.count(
                        crossAxisCount: MediaQuery.of(context).size.width > 600 ? 2 : 1,
                        shrinkWrap: true,
                        physics: const NeverScrollableScrollPhysics(),
                        mainAxisSpacing: 10,
                        crossAxisSpacing: 10,
                        childAspectRatio: MediaQuery.of(context).size.width > 600 ? 2.6 : 3.4,
                        children: _departments!
                            .map((d) => _ServiceCard(
                                  department: d,
                                  selected: d.id == _selectedId,
                                  onTap: d.isPaused ? null : () => setState(() => _selectedId = d.id),
                                ))
                            .toList(),
                      ),
                      if (_selected != null) ...[
                        const SizedBox(height: 12),
                        Banner2(
                          icon: Icons.schedule_rounded,
                          color: theme.colorScheme.primary,
                          text: '${_selected!.waitingCount} people waiting in ${_selected!.name}. '
                              'Estimated wait: ${formatEta(_selected!.estimatedWaitMinutes)}.',
                        ),
                      ],
                      const SizedBox(height: 24),
                      Text('2. Your details', style: theme.textTheme.titleMedium),
                      const SizedBox(height: 12),
                      TextFormField(
                        controller: _name,
                        textCapitalization: TextCapitalization.words,
                        textInputAction: TextInputAction.next,
                        maxLength: 80,
                        decoration: const InputDecoration(
                          labelText: 'Full name *',
                          prefixIcon: Icon(Icons.person_outline),
                          counterText: '',
                        ),
                        validator: (v) {
                          final s = (v ?? '').trim();
                          if (s.length < 2) return 'Please enter your name';
                          return null;
                        },
                      ),
                      const SizedBox(height: 12),
                      TextFormField(
                        controller: _phone,
                        keyboardType: TextInputType.phone,
                        decoration: const InputDecoration(
                          labelText: 'Mobile number (optional)',
                          prefixIcon: Icon(Icons.phone_outlined),
                        ),
                        validator: (v) {
                          final s = (v ?? '').replaceAll(RegExp(r'[\s\-()]'), '');
                          if (s.isEmpty) return null;
                          if (!RegExp(r'^\+?[0-9]{7,15}$').hasMatch(s)) return 'Enter a valid phone number';
                          return null;
                        },
                      ),
                      const SizedBox(height: 12),
                      Card(
                        elevation: 0,
                        color: Colors.purple.withAlpha(_priority ? 28 : 10),
                        child: Column(children: [
                          SwitchListTile(
                            value: _priority,
                            onChanged: (v) => setState(() => _priority = v),
                            secondary: const Icon(Icons.star_rounded, color: Colors.purple),
                            title: const Text('I need priority assistance'),
                            subtitle: const Text('Senior citizens, differently-abled, pregnant visitors or emergencies'),
                          ),
                          if (_priority)
                            Padding(
                              padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
                              child: SelectField<String>(
                                label: 'Reason *',
                                value: _priorityReason,
                                items: priorityReasons
                                    .map((r) => DropdownMenuItem(value: r, child: Text(r)))
                                    .toList(),
                                onChanged: (v) => setState(() => _priorityReason = v),
                                validator: (v) => _priority && (v == null || v.isEmpty) ? 'Please select a reason' : null,
                              ),
                            ),
                        ]),
                      ),
                      const SizedBox(height: 24),
                      FilledButton.icon(
                        style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(56)),
                        onPressed: _submitting ? null : _submit,
                        icon: _submitting
                            ? const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2))
                            : const Icon(Icons.confirmation_number_outlined),
                        label: Text(_selected == null ? 'Generate token' : 'Generate ${_selected!.name} token'),
                      ),
                    ]),
                  ),
                ),
              ],
            ),
    );
  }
}

class _ServiceCard extends StatelessWidget {
  const _ServiceCard({required this.department, required this.selected, required this.onTap});

  final Department department;
  final bool selected;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final d = department;
    final disabled = d.isPaused;
    return Opacity(
      opacity: disabled ? 0.6 : 1,
      child: Card(
        elevation: 0,
        margin: EdgeInsets.zero,
        color: selected ? scheme.primaryContainer : null,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(16),
          side: BorderSide(color: selected ? scheme.primary : scheme.outlineVariant, width: selected ? 2 : 1),
        ),
        child: InkWell(
          borderRadius: BorderRadius.circular(16),
          onTap: onTap,
          child: Padding(
            padding: const EdgeInsets.all(12),
            child: Row(children: [
              CircleAvatar(
                radius: 24,
                backgroundColor: selected ? scheme.primary : scheme.secondaryContainer,
                child: Icon(departmentIcon(d.code), color: selected ? scheme.onPrimary : scheme.onSecondaryContainer),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    Text(d.name,
                        style: Theme.of(context).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w600),
                        overflow: TextOverflow.ellipsis),
                    const SizedBox(height: 2),
                    Text(
                      disabled
                          ? 'Paused${d.pauseReason.isEmpty ? '' : ' · ${d.pauseReason}'}'
                          : '${d.waitingCount} waiting · ${formatEta(d.estimatedWaitMinutes)}',
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(color: disabled ? Colors.orange.shade800 : scheme.onSurfaceVariant),
                    ),
                  ],
                ),
              ),
              if (selected) Icon(Icons.check_circle, color: scheme.primary),
              if (disabled) const Icon(Icons.pause_circle_filled_rounded, color: Colors.orange),
            ]),
          ),
        ),
      ),
    );
  }
}
