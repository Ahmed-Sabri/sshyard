package policy

import "testing"

// diagnosisScript is a real troubleshooting script an assistant ran: every
// part only reads, so it must not ask.
const diagnosisScript = `printf '\n--- swap policy ---\n'; sysctl vm.swappiness; cat /proc/swaps; printf '\n--- PHP timeouts ---\n'; grep -nE '^[[:space:]]*(pm\.|request_terminate_timeout)' /www/server/php/84/etc/php-fpm.conf; printf '\n--- OOM ---\n'; journalctl -k --since '12:43:00' --until '12:44:00' --no-pager | awk '/kernel: \[/ && /php-fpm$/ {n++;kb+=$(NF-5)*4} END {print "php processes:",n,"anon RSS estimate KiB:",kb}'; tail -20000 /www/wwwlogs/site.log | awk '$4 ~ /05\/Nov\/2026:12:/ {n++; ip[$1]++; p=$7; sub(/\?.*/,"",p); path[p]++; if($7 ~ /[?&]s=/) search++} END {print "total",n,"search",search; for(k in ip)print "IP",ip[k],k; for(k in path)print "PATH",path[k],k}' | sort -k1,1 -k2,2nr | head -65; grep '05-Nov-2026 12:' /www/server/php/84/var/log/php-fpm.log | grep -E 'max_children|timed out' | head -12; uptime`

func TestDiagnosticCommands(t *testing.T) {
	e := NewEngine()
	cases := []struct {
		cmd  string
		cat  Category
		risk Risk
	}{
		{diagnosisScript, "read", RiskReadOnly},
		{`date; uptime; nproc; cat /proc/loadavg; free -h; vmstat 1 5; ps -eo pid,ppid,user,stat,pcpu,pmem,comm,wchan:28,args --sort=-pcpu | head -35`, "read", RiskReadOnly},
		{`top -b -d 1 -n 2 -o %CPU | awk '/^top -/{n++} n==2 {if (++lines<=17) print}'`, "read", RiskReadOnly},
		{"ps -o pid= -p 1", "read", RiskReadOnly},
		{"ps -u root -o pid,args", "read", RiskReadOnly},
		{"ps -o pid,secret", "unknown", RiskUnknown},
		{"journalctl -u nginx --since today -p err -b -1 --no-pager", "read", RiskReadOnly},
		{"journalctl --vacuum-time=1d", "unknown", RiskUnknown},
		{"journalctl --rotate", "unknown", RiskUnknown},
		{"sysctl vm.swappiness", "read", RiskReadOnly},
		{"sysctl -a", "read", RiskReadOnly},
		{"sysctl vm.swappiness=10", "unknown", RiskUnknown},
		{"sysctl -w vm.swappiness=10", "unknown", RiskUnknown},
		{"sysctl -p", "unknown", RiskUnknown},
		{"dmesg -T | tail -50", "read", RiskReadOnly},
		{"dmesg -c", "unknown", RiskUnknown},
		{"dmesg -n 1", "unknown", RiskUnknown},
		{"vmstat 1 5", "read", RiskReadOnly},
		{"iostat -x 1 3", "read", RiskReadOnly},
		{"lsof -i :80", "read", RiskReadOnly},
		{"lsof /home/user/.ssh/id_rsa", "sensitive-read", RiskMedium},
		{"last -n 10", "read", RiskReadOnly},
		{"pidstat -u 1 3", "read", RiskReadOnly},
		{"pidstat -e sh", "unknown", RiskUnknown},
		{"cut -d' ' -f1 /var/log/nginx/access.log", "read", RiskReadOnly},
		{"cut -f1 /tmp/a", "sensitive-read", RiskMedium},
		// awk
		{`awk '{print $1}' /var/log/nginx/access.log | sort | uniq -c | sort -nr | head`, "read", RiskReadOnly},
		{`awk -F: '$3 > 999 {print $1}' /etc/passwd`, "read", RiskReadOnly},
		{`awk -v n=3 'NR <= n' /etc/hostname`, "read", RiskReadOnly},
		{`awk '{print (a > b)}'`, "read", RiskReadOnly},
		{`awk '/a|b/ {n++} END {print n || 0}'`, "read", RiskReadOnly},
		{`awk '{print}' /tmp/a`, "sensitive-read", RiskMedium},
		{`awk '{print ENVIRON["HOME"]}'`, "sensitive-read", RiskMedium},
		{`awk 'BEGIN {system("id")}'`, "unknown", RiskUnknown},
		{`awk '{print | "sh"}'`, "unknown", RiskUnknown},
		{`awk '{print $0 > "/tmp/out"}'`, "unknown", RiskUnknown},
		{`awk '{printf("%s", $0) >> "/tmp/out"}'`, "unknown", RiskUnknown},
		{`awk '{"date" | getline d}'`, "unknown", RiskUnknown},
		{`awk '{getline line < "/etc/shadow"}'`, "unknown", RiskUnknown},
		{`awk '@include "x"'`, "unknown", RiskUnknown},
		{`awk -f prog.awk /etc/hostname`, "unknown", RiskUnknown},
		{`awk --exec prog.awk`, "unknown", RiskUnknown},
		{`awk '{print "unterminated}'`, "unknown", RiskUnknown},
		// Division is never read as a regex that could hide code.
		{`awk '{x = a++ / 1; system("id"); y = 1 / 1}'`, "unknown", RiskUnknown},
		{"awk '{x = a \\\n/ 1; system(\"id\"); y = 1 / 1}'", "unknown", RiskUnknown},
		{`awk '{x = $1 / 2; print x}'`, "read", RiskReadOnly},
		// Servers and interpreters.
		{"nginx -t", "read", RiskReadOnly},
		{"nginx -T", "read", RiskReadOnly},
		{"nginx -s reload", "service", RiskMedium},
		{"nginx", "service", RiskMedium},
		{"nginx -x", "unknown", RiskUnknown},
		{"apachectl configtest", "read", RiskReadOnly},
		{"apachectl -S", "read", RiskReadOnly},
		{"apachectl graceful", "service", RiskMedium},
		{"httpd -k restart", "service", RiskMedium},
		{"php-fpm8.4 -t", "read", RiskReadOnly},
		{"php-fpm -i", "sensitive-read", RiskMedium},
		{"php -v", "read", RiskReadOnly},
		{"php -m", "read", RiskReadOnly},
		{"python3 -V", "read", RiskReadOnly},
		{"php -i", "unknown", RiskUnknown},
		{"php -r 'echo 1;'", "unknown", RiskUnknown},
	}
	for _, c := range cases {
		t.Run(c.cmd, func(t *testing.T) {
			g := e.Classify(cmdAction(c.cmd), Scope{Home: "/home/user"})
			if g.Category != c.cat || g.Risk != c.risk {
				t.Fatalf("got %s %s (%s); want %s %s", g.Category, g.Risk, g.Why, c.cat, c.risk)
			}
		})
	}
}

func TestSingleCommand(t *testing.T) {
	for cmd, want := range map[string]bool{
		"systemctl restart nginx": true, "  ls -la /tmp ": true,
		"systemctl restart nginx; ls": false, "a && b": false, "a | b": false,
		"a > /tmp/x": false, "a &": false, "a $(b)": false, "a $X": false,
		"a <(b)": false, "! a": false, "": false, "(a)": false, "a $((1+1))": false,
	} {
		if got := singleCommand(cmd); got != want {
			t.Errorf("singleCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}
