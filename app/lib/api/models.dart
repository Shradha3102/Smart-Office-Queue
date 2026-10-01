// Data models mirroring the Go API's JSON.

DateTime? _date(dynamic v) =>
    v is String && v.isNotEmpty ? DateTime.tryParse(v)?.toLocal() : null;

int _int(dynamic v, [int fallback = 0]) =>
    v is num ? v.toInt() : (int.tryParse('$v') ?? fallback);

int? _intOrNull(dynamic v) => v is num ? v.toInt() : null;

double _double(dynamic v) => v is num ? v.toDouble() : 0;

String _str(dynamic v) => v is String ? v : '';

String? _strOrNull(dynamic v) => v is String ? v : null;

class Department {
  Department({
    required this.id,
    required this.code,
    required this.name,
    required this.description,
    required this.avgServiceMinutes,
    required this.isPaused,
    required this.pauseReason,
    required this.waitingCount,
    required this.nowServing,
    required this.effectiveServiceMinutes,
    required this.estimatedWaitMinutes,
  });

  final int id;
  final String code;
  final String name;
  final String description;
  final int avgServiceMinutes;
  final bool isPaused;
  final String pauseReason;
  final int waitingCount;
  final String? nowServing;
  final double effectiveServiceMinutes;
  final int estimatedWaitMinutes;

  factory Department.fromJson(Map<String, dynamic> j) => Department(
        id: _int(j['id']),
        code: _str(j['code']),
        name: _str(j['name']),
        description: _str(j['description']),
        avgServiceMinutes: _int(j['avg_service_minutes'], 5),
        isPaused: j['is_paused'] == true,
        pauseReason: _str(j['pause_reason']),
        waitingCount: _int(j['waiting_count']),
        nowServing: _strOrNull(j['now_serving']),
        effectiveServiceMinutes: _double(j['effective_service_minutes']),
        estimatedWaitMinutes: _int(j['estimated_wait_minutes']),
      );
}

class TokenStatusValue {
  static const waiting = 'waiting';
  static const serving = 'serving';
  static const completed = 'completed';
  static const cancelled = 'cancelled';
  static const noShow = 'no_show';
}

class QueueToken {
  QueueToken({
    required this.id,
    required this.tokenNumber,
    required this.departmentId,
    required this.departmentCode,
    required this.departmentName,
    required this.visitorName,
    required this.visitorPhone,
    required this.isPriority,
    required this.priorityReason,
    required this.inPriorityLane,
    required this.status,
    required this.noShowCount,
    required this.transferCount,
    required this.transferredFrom,
    required this.source,
    required this.closeReason,
    required this.createdAt,
    required this.enqueuedAt,
    required this.calledAt,
    required this.completedAt,
    required this.closedAt,
    required this.position,
    required this.peopleAhead,
    required this.estimatedWaitMinutes,
  });

  final String id;
  final String tokenNumber;
  final int departmentId;
  final String departmentCode;
  final String departmentName;
  final String visitorName;
  final String visitorPhone;
  final bool isPriority;
  final String priorityReason;
  final bool inPriorityLane;
  final String status;
  final int noShowCount;
  final int transferCount;
  final String transferredFrom;
  final String source;
  final String closeReason;
  final DateTime? createdAt;
  final DateTime? enqueuedAt;
  final DateTime? calledAt;
  final DateTime? completedAt;
  final DateTime? closedAt;
  final int? position;
  final int? peopleAhead;
  final int? estimatedWaitMinutes;

  bool get isWaiting => status == TokenStatusValue.waiting;
  bool get isServing => status == TokenStatusValue.serving;
  bool get isActive => isWaiting || isServing;

  factory QueueToken.fromJson(Map<String, dynamic> j) => QueueToken(
        id: _str(j['id']),
        tokenNumber: _str(j['token_number']),
        departmentId: _int(j['department_id']),
        departmentCode: _str(j['department_code']),
        departmentName: _str(j['department_name']),
        visitorName: _str(j['visitor_name']),
        visitorPhone: _str(j['visitor_phone']),
        isPriority: j['is_priority'] == true,
        priorityReason: _str(j['priority_reason']),
        inPriorityLane: j['in_priority_lane'] == true,
        status: _str(j['status']),
        noShowCount: _int(j['no_show_count']),
        transferCount: _int(j['transfer_count']),
        transferredFrom: _str(j['transferred_from']),
        source: _str(j['source']),
        closeReason: _str(j['close_reason']),
        createdAt: _date(j['created_at']),
        enqueuedAt: _date(j['enqueued_at']),
        calledAt: _date(j['called_at']),
        completedAt: _date(j['completed_at']),
        closedAt: _date(j['closed_at']),
        position: _intOrNull(j['position']),
        peopleAhead: _intOrNull(j['people_ahead']),
        estimatedWaitMinutes: _intOrNull(j['estimated_wait_minutes']),
      );
}

class TokenStatus {
  TokenStatus({
    required this.token,
    required this.departmentId,
    required this.departmentCode,
    required this.departmentName,
    required this.departmentPaused,
    required this.pauseReason,
    required this.nowServing,
    this.accessSecret,
  });

  final QueueToken token;
  final int departmentId;
  final String departmentCode;
  final String departmentName;
  final bool departmentPaused;
  final String pauseReason;
  final String? nowServing;

  /// Only present in the response that created the token.
  final String? accessSecret;

  factory TokenStatus.fromJson(Map<String, dynamic> j) {
    final d = (j['department'] as Map?)?.cast<String, dynamic>() ?? const {};
    return TokenStatus(
      token: QueueToken.fromJson((j['token'] as Map).cast<String, dynamic>()),
      departmentId: _int(d['id']),
      departmentCode: _str(d['code']),
      departmentName: _str(d['name']),
      departmentPaused: d['is_paused'] == true,
      pauseReason: _str(d['pause_reason']),
      nowServing: _strOrNull(j['now_serving']),
      accessSecret: _strOrNull(j['access_secret']),
    );
  }
}

class QueueView {
  QueueView({
    required this.department,
    required this.serving,
    required this.waiting,
    required this.recent,
  });

  final Department department;
  final QueueToken? serving;
  final List<QueueToken> waiting;
  final List<QueueToken> recent;

  factory QueueView.fromJson(Map<String, dynamic> j) => QueueView(
        department: Department.fromJson((j['department'] as Map).cast<String, dynamic>()),
        serving: j['serving'] is Map
            ? QueueToken.fromJson((j['serving'] as Map).cast<String, dynamic>())
            : null,
        waiting: _tokens(j['waiting']),
        recent: _tokens(j['recent']),
      );

  static List<QueueToken> _tokens(dynamic v) => v is List
      ? v.map((e) => QueueToken.fromJson((e as Map).cast<String, dynamic>())).toList()
      : <QueueToken>[];
}

class NoShowResult {
  NoShowResult(this.token, this.action);
  final QueueToken token;
  final String action; // requeued | cancelled

  factory NoShowResult.fromJson(Map<String, dynamic> j) => NoShowResult(
        QueueToken.fromJson((j['token'] as Map).cast<String, dynamic>()),
        _str(j['action']),
      );
}

class DeptStats {
  DeptStats.fromJson(Map<String, dynamic> j)
      : departmentId = _int(j['department_id']),
        code = _str(j['code']),
        name = _str(j['name']),
        isPaused = j['is_paused'] == true,
        pauseReason = _str(j['pause_reason']),
        waiting = _int(j['waiting']),
        priorityWaiting = _int(j['priority_waiting']),
        serving = _int(j['serving']),
        nowServing = _strOrNull(j['now_serving']),
        issued = _int(j['issued']),
        called = _int(j['called']),
        completed = _int(j['completed']),
        noShows = _int(j['no_shows']),
        autoCancelledNoShows = _int(j['auto_cancelled_no_shows']),
        cancelled = _int(j['cancelled']),
        transferredOut = _int(j['transferred_out']),
        avgWaitMinutes = _double(j['avg_wait_minutes']),
        avgServiceMinutes = _double(j['avg_service_minutes']),
        longestWaitingMinutes = _double(j['longest_waiting_minutes']);

  final int departmentId;
  final String code;
  final String name;
  final bool isPaused;
  final String pauseReason;
  final int waiting;
  final int priorityWaiting;
  final int serving;
  final String? nowServing;
  final int issued;
  final int called;
  final int completed;
  final int noShows;
  final int autoCancelledNoShows;
  final int cancelled;
  final int transferredOut;
  final double avgWaitMinutes;
  final double avgServiceMinutes;
  final double longestWaitingMinutes;
}

class DashboardData {
  DashboardData({required this.date, required this.timezone, required this.totals, required this.departments});

  final String date;
  final String timezone;
  final DeptStats totals;
  final List<DeptStats> departments;

  factory DashboardData.fromJson(Map<String, dynamic> j) => DashboardData(
        date: _str(j['date']),
        timezone: _str(j['timezone']),
        totals: DeptStats.fromJson((j['totals'] as Map?)?.cast<String, dynamic>() ?? const {}),
        departments: (j['departments'] as List? ?? const [])
            .map((e) => DeptStats.fromJson((e as Map).cast<String, dynamic>()))
            .toList(),
      );
}

class StaffUser {
  StaffUser({
    required this.id,
    required this.name,
    required this.email,
    required this.role,
    required this.departmentId,
    this.departmentName,
    this.isActive = true,
  });

  final int id;
  final String name;
  final String email;
  final String role;
  final int? departmentId;
  final String? departmentName;
  final bool isActive;

  bool get isAdmin => role == 'admin';

  factory StaffUser.fromJson(Map<String, dynamic> j) => StaffUser(
        id: _int(j['id']),
        name: _str(j['name']),
        email: _str(j['email']),
        role: _str(j['role']),
        departmentId: _intOrNull(j['department_id']),
        departmentName: _strOrNull(j['department_name']),
        isActive: j['is_active'] != false,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'email': email,
        'role': role,
        'department_id': departmentId,
      };
}

class LoginResult {
  LoginResult(this.token, this.expiresAt, this.user);
  final String token;
  final DateTime? expiresAt;
  final StaffUser user;

  factory LoginResult.fromJson(Map<String, dynamic> j) => LoginResult(
        _str(j['token']),
        _date(j['expires_at']),
        StaffUser.fromJson((j['user'] as Map).cast<String, dynamic>()),
      );
}
