import 'package:flutter_test/flutter_test.dart';
import 'package:smart_queue/api/models.dart';
import 'package:smart_queue/widgets/common.dart';

void main() {
  test('formatEta renders friendly durations', () {
    expect(formatEta(null), '-');
    expect(formatEta(0), 'Any moment');
    expect(formatEta(7), '~7 min');
    expect(formatEta(60), '~1 h');
    expect(formatEta(95), '~1 h 35 min');
  });

  test('TokenStatus parses API payload', () {
    final st = TokenStatus.fromJson({
      'token': {
        'id': 'a1b2',
        'token_number': 'IT-004',
        'department_id': 1,
        'department_code': 'IT',
        'department_name': 'IT Support',
        'visitor_name': 'Asha',
        'is_priority': true,
        'priority_reason': 'Senior citizen',
        'in_priority_lane': true,
        'status': 'waiting',
        'no_show_count': 1,
        'created_at': '2026-10-01T09:47:23.886048Z',
        'position': 2,
        'people_ahead': 1,
        'estimated_wait_minutes': 6,
      },
      'department': {'id': 1, 'code': 'IT', 'name': 'IT Support', 'is_paused': false, 'pause_reason': ''},
      'now_serving': 'IT-002',
      'access_secret': 'secret',
    });
    expect(st.token.tokenNumber, 'IT-004');
    expect(st.token.isWaiting, isTrue);
    expect(st.token.position, 2);
    expect(st.token.estimatedWaitMinutes, 6);
    expect(st.nowServing, 'IT-002');
    expect(st.accessSecret, 'secret');
    expect(st.token.createdAt, isNotNull);
  });
}
