#!/usr/bin/env python3
"""
End-to-end walkthrough of every queue rule against a running API (stdlib only).

    python3 scripts/demo_scenario.py [http://localhost:8080]

Uses the seeded demo accounts. It pauses/resumes departments and creates tokens,
so run it against a demo/dev database, not production.
"""
import json
import sys
import urllib.error
import urllib.request

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080").rstrip("/")
failures = 0


def call(method, path, body=None, token=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            return r.status, json.loads(r.read() or b"null")
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"null")


def check(label, cond, detail=""):
    global failures
    print(("  PASS  " if cond else "  FAIL  ") + label + (f"   [{detail}]" if detail and not cond else ""))
    if not cond:
        failures += 1


def login(email, pw="Staff@123"):
    s, r = call("POST", "/api/auth/login", {"email": email, "password": pw})
    assert s == 200, r
    return r["token"]


def new_token(dept, name, priority=False):
    s, r = call("POST", "/api/tokens", {"department_id": dept, "visitor_name": name,
                                         "is_priority": priority,
                                         "priority_reason": "Senior citizen" if priority else ""})
    assert s == 201, r
    return r


def waiting(dept, tok):
    return [t["token_number"] for t in call("GET", f"/api/staff/departments/{dept}/queue", token=tok)[1]["waiting"]]


def clear(dept, tok):
    """Finish any visitor at the counter and cancel everyone waiting, so the demo starts clean."""
    q = call("GET", f"/api/staff/departments/{dept}/queue", token=tok)[1]
    if q["department"]["is_paused"]:
        call("POST", f"/api/staff/departments/{dept}/resume", {}, tok)
    if q["serving"]:
        call("POST", f"/api/staff/tokens/{q['serving']['id']}/complete", token=tok)
    for t in q["waiting"]:
        call("POST", f"/api/staff/tokens/{t['id']}/cancel", {"reason": "demo reset"}, tok)


print(f"Server: {BASE}")
depts = {d["code"]: d["id"] for d in call("GET", "/api/departments")[1]}
admin = login("admin@office.local", "Admin@123")
it, hr = login("it@office.local"), login("hr@office.local")
IT, HR = depts["IT"], depts["HR"]
clear(IT, admin)
clear(HR, admin)

print("\n1) Token generation, numbering, position & ETA")
a, b, c = new_token(IT, "Asha"), new_token(IT, "Bhavin"), new_token(IT, "Chirag")
nums = [x["token"]["token_number"] for x in (a, b, c)]
check("numbers look like IT-###", all(n.startswith("IT-") and len(n) >= 6 for n in nums), nums)
check("positions are 1,2,3", [x["token"]["position"] for x in (a, b, c)] == [1, 2, 3])
check("ETA grows with position", a["token"]["estimated_wait_minutes"] < c["token"]["estimated_wait_minutes"])

print("\n2) Priority goes ahead but never interrupts the visitor being served")
s, served = call("POST", f"/api/staff/departments/{IT}/call-next", token=it)
check("call next serves the first token", s == 200 and served["token_number"] == nums[0])
p = new_token(IT, "Priya (priority)", priority=True)
check("priority token is first in line", p["token"]["position"] == 1)
s, r = call("POST", f"/api/staff/departments/{IT}/call-next", token=it)
check("cannot call next while someone is being served", s == 409 and r["error"]["code"] == "already_serving")
call("POST", f"/api/staff/tokens/{served['id']}/complete", token=it)
s, nxt = call("POST", f"/api/staff/departments/{IT}/call-next", token=it)
check("after completing, the priority token is called", nxt["token_number"] == p["token"]["token_number"])
call("POST", f"/api/staff/tokens/{nxt['id']}/complete", token=it)

print("\n3) No-show: first → end of queue, second → auto-cancel")
s, cur = call("POST", f"/api/staff/departments/{IT}/call-next", token=it)  # Bhavin
s, r = call("POST", f"/api/staff/tokens/{cur['id']}/no-show", token=it)
check("first no-show requeues", r["action"] == "requeued")
check("…to the end of the queue", waiting(IT, it)[-1] == cur["token_number"], waiting(IT, it))
s, c2 = call("POST", f"/api/staff/departments/{IT}/call-next", token=it)   # Chirag
call("POST", f"/api/staff/tokens/{c2['id']}/complete", token=it)
s, again = call("POST", f"/api/staff/departments/{IT}/call-next", token=it)
s, r = call("POST", f"/api/staff/tokens/{again['id']}/no-show", token=it)
check("second no-show cancels automatically", r["action"] == "cancelled" and r["token"]["status"] == "no_show")

print("\n4) Pause / resume")
call("POST", f"/api/staff/departments/{HR}/pause", {"reason": "Lunch"}, hr)
s, r = call("POST", "/api/tokens", {"department_id": HR, "visitor_name": "Late visitor"})
check("token generation blocked while paused", s == 409 and r["error"]["code"] == "department_paused")
call("POST", f"/api/staff/departments/{HR}/resume", {}, hr)
s, r = call("POST", "/api/tokens", {"department_id": HR, "visitor_name": "After resume"})
check("works again after resume", s == 201)

print("\n5) Transfer")
t = new_token(IT, "Dev (needs HR)")
s, moved = call("POST", f"/api/staff/tokens/{t['token']['id']}/transfer", {"department_id": HR}, it)
check("transferred with a new HR number", s == 200 and moved["token_number"].startswith("HR-"), moved)
check("remembers its old number", moved.get("transferred_from") == t["token"]["token_number"])
s, st = call("GET", f"/api/tokens/{t['token']['id']}")
check("visitor tracking follows the token", st["department"]["code"] == "HR" and st["token"]["position"] >= 1)

print("\n6) Visitor cancellation")
x = new_token(IT, "Esha")
s, _ = call("POST", f"/api/tokens/{x['token']['id']}/cancel", {"access_secret": "wrong"})
check("wrong secret is rejected", s == 403)
s, r = call("POST", f"/api/tokens/{x['token']['id']}/cancel", {"access_secret": x["access_secret"]})
check("owner can cancel", s == 200 and r["token"]["status"] == "cancelled")

print("\n7) Permissions & dashboard")
s, _ = call("POST", f"/api/staff/departments/{IT}/call-next", token=hr)
check("HR staff cannot operate the IT queue", s == 403)
s, d = call("GET", "/api/staff/dashboard", token=admin)
tot = d["totals"]
check("dashboard has all headline figures",
      all(k in tot for k in ("waiting", "serving", "completed", "no_shows", "avg_wait_minutes")), tot)
print(f"        today: waiting={tot['waiting']} serving={tot['serving']} completed={tot['completed']} "
      f"no-shows={tot['no_shows']} avg wait={tot['avg_wait_minutes']} min")

print("\n" + ("ALL CHECKS PASSED" if failures == 0 else f"{failures} CHECK(S) FAILED"))
sys.exit(1 if failures else 0)
