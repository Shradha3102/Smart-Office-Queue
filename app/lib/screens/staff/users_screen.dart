import 'package:flutter/material.dart';

import '../../api/models.dart';
import '../../services/app_services.dart';
import '../../widgets/common.dart';

/// Admin: manage staff accounts.
class UsersScreen extends StatefulWidget {
  const UsersScreen({super.key});

  @override
  State<UsersScreen> createState() => _UsersScreenState();
}

class _UsersScreenState extends State<UsersScreen> {
  List<StaffUser>? _users;
  List<Department> _departments = [];
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final results = await Future.wait([services.api.users(), services.api.departments()]);
      if (!mounted) return;
      setState(() {
        _users = results[0] as List<StaffUser>;
        _departments = results[1] as List<Department>;
        _error = null;
      });
    } catch (e) {
      if (mounted) setState(() => _error = errorText(e));
    }
  }

  Future<void> _toggle(StaffUser u) async {
    try {
      await services.api.setUserActive(u.id, !u.isActive);
      if (mounted) showSnack(context, '${u.name} ${u.isActive ? 'deactivated' : 'activated'}');
      _load();
    } catch (e) {
      if (mounted) showSnack(context, errorText(e), error: true);
    }
  }

  Future<void> _add() async {
    final created = await showDialog<bool>(
      context: context,
      builder: (_) => _AddUserDialog(departments: _departments),
    );
    if (created == true) {
      if (mounted) showSnack(context, 'User created');
      _load();
    }
  }

  @override
  Widget build(BuildContext context) {
    final users = _users;
    return Scaffold(
      floatingActionButton: FloatingActionButton.extended(
        heroTag: 'add-user',
        onPressed: _add,
        icon: const Icon(Icons.person_add_alt_1),
        label: const Text('Add staff'),
      ),
      body: users == null
          ? (_error != null
              ? ErrorRetry(message: _error!, onRetry: _load)
              : const Center(child: CircularProgressIndicator()))
          : RefreshIndicator(
              onRefresh: _load,
              child: ListView.builder(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 96),
                itemCount: users.length,
                itemBuilder: (context, i) {
                  final u = users[i];
                  final me = services.session.user?.id == u.id;
                  return MaxWidth(
                    width: 800,
                    child: Card(
                      elevation: 0,
                      shape: RoundedRectangleBorder(
                        borderRadius: BorderRadius.circular(14),
                        side: BorderSide(color: Theme.of(context).colorScheme.outlineVariant),
                      ),
                      child: ListTile(
                        leading: CircleAvatar(
                          child: Icon(u.isAdmin ? Icons.admin_panel_settings : Icons.person_outline),
                        ),
                        title: Text(u.name + (me ? ' (you)' : '')),
                        subtitle: Text('${u.email}\n${u.isAdmin ? 'Administrator · all departments' : (u.departmentName ?? 'Staff')}'),
                        isThreeLine: true,
                        trailing: Switch(
                          value: u.isActive,
                          onChanged: me ? null : (_) => _toggle(u),
                        ),
                      ),
                    ),
                  );
                },
              ),
            ),
    );
  }
}

class _AddUserDialog extends StatefulWidget {
  const _AddUserDialog({required this.departments});
  final List<Department> departments;

  @override
  State<_AddUserDialog> createState() => _AddUserDialogState();
}

class _AddUserDialogState extends State<_AddUserDialog> {
  final _formKey = GlobalKey<FormState>();
  final _name = TextEditingController();
  final _email = TextEditingController();
  final _password = TextEditingController();
  String _role = 'staff';
  int? _dept;
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _name.dispose();
    _email.dispose();
    _password.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (!(_formKey.currentState?.validate() ?? false)) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await services.api.createUser(
        name: _name.text.trim(),
        email: _email.text.trim(),
        password: _password.text,
        role: _role,
        departmentId: _role == 'staff' ? _dept : null,
      );
      if (mounted) Navigator.pop(context, true);
    } catch (e) {
      if (mounted) {
        setState(() {
          _error = errorText(e);
          _busy = false;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Add staff account'),
      content: SizedBox(
        width: 420,
        child: Form(
          key: _formKey,
          child: SingleChildScrollView(
            child: Column(mainAxisSize: MainAxisSize.min, children: [
              TextFormField(
                controller: _name,
                decoration: const InputDecoration(labelText: 'Full name'),
                validator: (v) => (v ?? '').trim().length < 2 ? 'Enter a name' : null,
              ),
              const SizedBox(height: 12),
              TextFormField(
                controller: _email,
                keyboardType: TextInputType.emailAddress,
                decoration: const InputDecoration(labelText: 'Email'),
                validator: (v) => (v ?? '').contains('@') ? null : 'Enter a valid email',
              ),
              const SizedBox(height: 12),
              TextFormField(
                controller: _password,
                obscureText: true,
                decoration: const InputDecoration(labelText: 'Password (min 8 characters)'),
                validator: (v) => (v ?? '').length < 8 ? 'At least 8 characters' : null,
              ),
              const SizedBox(height: 12),
              SegmentedButton<String>(
                segments: const [
                  ButtonSegment(value: 'staff', label: Text('Staff'), icon: Icon(Icons.badge_outlined)),
                  ButtonSegment(value: 'admin', label: Text('Admin'), icon: Icon(Icons.admin_panel_settings_outlined)),
                ],
                selected: {_role},
                onSelectionChanged: (s) => setState(() => _role = s.first),
              ),
              if (_role == 'staff') ...[
                const SizedBox(height: 12),
                SelectField<int>(
                  label: 'Department',
                  value: _dept,
                  items: widget.departments.map((d) => DropdownMenuItem(value: d.id, child: Text(d.name))).toList(),
                  onChanged: (v) => setState(() => _dept = v),
                  validator: (v) => v == null ? 'Choose a department' : null,
                ),
              ],
              if (_error != null) ...[
                const SizedBox(height: 8),
                Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
              ],
            ]),
          ),
        ),
      ),
      actions: [
        TextButton(onPressed: _busy ? null : () => Navigator.pop(context), child: const Text('Cancel')),
        FilledButton(onPressed: _busy ? null : _submit, child: Text(_busy ? 'Saving…' : 'Create')),
      ],
    );
  }
}
