//! Real routed packet regression. Run ONLY in the disposable --network none
//! Docker image described in Dockerfile.kernel-test; never on a live gateway.
#![cfg(target_os = "linux")]
use std::io::{BufRead, BufReader, Write};
use std::process::{Child, Command, Stdio};
use std::thread::sleep;
use std::time::Duration;
use yummi_gateway::config::GatewayConfig;
use yummi_gateway::network::NftablesController;
use yummi_gateway::policy::rules::EgressMode;

fn run(args: &[&str]) {
    let out = Command::new(args[0]).args(&args[1..]).output().unwrap();
    assert!(
        out.status.success(),
        "{args:?}: {}",
        String::from_utf8_lossy(&out.stderr)
    );
}

fn setup(v6: bool) {
    run(&["sysctl", "-qw", "net.ipv4.ip_forward=1"]);
    run(&["sysctl", "-qw", "net.ipv6.conf.all.forwarding=1"]);
    for (name, n) in [("sb", 91), ("peer", 92), ("other", 93)] {
        run(&["ip", "netns", "add", name]);
        run(&[
            "ip",
            "link",
            "add",
            &format!("r{n}"),
            "type",
            "veth",
            "peer",
            "name",
            &format!("c{n}"),
        ]);
        run(&["ip", "link", "set", &format!("c{n}"), "netns", name]);
        run(&["ip", "link", "set", &format!("r{n}"), "up"]);
        run(&["ip", "netns", "exec", name, "ip", "link", "set", "lo", "up"]);
        run(&[
            "ip",
            "netns",
            "exec",
            name,
            "ip",
            "link",
            "set",
            &format!("c{n}"),
            "up",
        ]);
        let (family, router, client, cidr) = if v6 {
            ("-6", format!("fd{n}::1"), format!("fd{n}::2"), "64")
        } else {
            ("-4", format!("10.{n}.0.1"), format!("10.{n}.0.2"), "24")
        };
        let mut address = vec!["ip", family, "addr", "add"];
        let rp = format!("{router}/{cidr}");
        let ri = format!("r{n}");
        address.extend([&rp, "dev", &ri]);
        if v6 {
            address.push("nodad");
        }
        run(&address);
        let cp = format!("{client}/{cidr}");
        let ci = format!("c{n}");
        let mut address = vec![
            "ip", "netns", "exec", name, "ip", family, "addr", "add", &cp, "dev", &ci,
        ];
        if v6 {
            address.push("nodad");
        }
        run(&address);
        run(&[
            "ip", "netns", "exec", name, "ip", family, "route", "add", "default", "via", &router,
        ]);
    }
    let blocked = if v6 { "fd92::3/64" } else { "10.92.0.3/24" };
    let mut args = vec![
        "ip", "netns", "exec", "peer", "ip", "addr", "add", blocked, "dev", "c92",
    ];
    if v6 {
        args.push("nodad");
    }
    run(&args);
}

struct Service(Child);
impl Drop for Service {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}
fn server(v6: bool) -> Service {
    let code = r#"
import socket,threading,sys
family=socket.AF_INET6 if sys.argv[1]=='true' else socket.AF_INET
host='fd92::2' if family==socket.AF_INET6 else '10.92.0.2'
blocked='fd92::3' if family==socket.AF_INET6 else '10.92.0.3'
def udp(s):
 while True:
  b,a=s.recvfrom(65535); s.sendto(b,a)
def stream(c):
 try:
  while True:
   b=c.recv(1024)
   if not b: break
   c.sendall(b)
 except OSError: pass
 finally: c.close()
for h in [host,blocked]:
 for p in [53,5353]:
  u=socket.socket(family,socket.SOCK_DGRAM); u.bind((h,p))
  threading.Thread(target=udp,args=(u,),daemon=True).start()
s=socket.socket(family); s.bind((host,8080)); s.listen()
print('ready',flush=True)
while True:
 c,a=s.accept(); threading.Thread(target=stream,args=(c,),daemon=True).start()
"#;
    let mut c = Command::new("ip")
        .args([
            "netns",
            "exec",
            "peer",
            "python3",
            "-u",
            "-c",
            code,
            if v6 { "true" } else { "false" },
        ])
        .stdout(Stdio::piped())
        .spawn()
        .unwrap();
    let mut ready = String::new();
    BufReader::new(c.stdout.take().unwrap())
        .read_line(&mut ready)
        .unwrap();
    assert_eq!(ready.trim(), "ready");
    Service(c)
}

fn udp(ns: &str, dest: &str, port: u16, count: usize) -> usize {
    let code = r#"
import socket,sys,time
host,port,count=sys.argv[1],int(sys.argv[2]),int(sys.argv[3])
s=socket.socket(socket.AF_INET6 if ':' in host else socket.AF_INET,socket.SOCK_DGRAM)
s.settimeout(.04); s.connect((host,port))
received=0
for n in range(count):
 try:
  s.send(str(n).encode())
  if s.recv(1024)==str(n).encode(): received+=1
 except OSError: pass
print(received)
"#;
    let out = Command::new("ip")
        .args([
            "netns",
            "exec",
            ns,
            "python3",
            "-c",
            code,
            dest,
            &port.to_string(),
            &count.to_string(),
        ])
        .output()
        .unwrap();
    assert!(
        out.status.success(),
        "{}",
        String::from_utf8_lossy(&out.stderr)
    );
    String::from_utf8(out.stdout)
        .unwrap()
        .trim()
        .parse()
        .unwrap()
}

struct Stream {
    service: Service,
    output: BufReader<std::process::ChildStdout>,
}
impl Stream {
    fn new(dest: &str) -> Self {
        let code = r#"
import socket,sys
s=socket.create_connection((sys.argv[1],8080),timeout=.4)
print('ready',flush=True)
for line in sys.stdin:
 try:
  s.sendall(line.encode()); print('ok' if s.recv(1024)==line.encode() else 'blocked',flush=True)
 except OSError: print('blocked',flush=True)
"#;
        let mut c = Command::new("ip")
            .args(["netns", "exec", "sb", "python3", "-u", "-c", code, dest])
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .spawn()
            .unwrap();
        let mut output = BufReader::new(c.stdout.take().unwrap());
        let mut ready = String::new();
        output.read_line(&mut ready).unwrap();
        assert_eq!(ready.trim(), "ready");
        Self {
            service: Service(c),
            output,
        }
    }
    fn probe(&mut self) -> bool {
        writeln!(self.service.0.stdin.as_mut().unwrap(), "probe").unwrap();
        let mut line = String::new();
        self.output.read_line(&mut line).unwrap();
        line.trim() == "ok"
    }
}

fn exercise(v6: bool) {
    eprintln!("running routed IPv{} matrix", if v6 { 6 } else { 4 });
    setup(v6);
    let _server = server(v6);
    let (sandbox, peer, blocked, prefix) = if v6 {
        ("fd91::2", "fd92::2", "fd92::3", "128")
    } else {
        ("10.91.0.2", "10.92.0.2", "10.92.0.3", "32")
    };
    let mut cfg: GatewayConfig =
        serde_yaml::from_str(include_str!("../config/default.yaml")).unwrap();
    cfg.network.sandbox_ip = sandbox.into();
    cfg.network.ttl_normalize = Some(64);
    cfg.scope.allow.clear();
    cfg.scope.block = vec![format!("{blocked}/{prefix}").parse().unwrap()];
    // Wait for real IPv6 neighbor discovery before any negative assertion.
    for ns in ["peer", "other", "sb"] {
        let mut reachable = false;
        for _ in 0..30 {
            if udp(ns, peer, 5353, 2) == 2 {
                reachable = true;
                break;
            }
            sleep(Duration::from_millis(50));
        }
        assert!(reachable, "unfiltered {ns} topology unavailable");
    }
    let ctrl = NftablesController::new(&cfg);
    ctrl.install_rules().unwrap();
    // Exceeds nft's default five-packet log burst. Any response is a leak.
    assert_eq!(
        udp("sb", peer, 5353, 32),
        0,
        "block_all leaked packets after log limiter exhaustion"
    );
    assert_eq!(udp("sb", peer, 53, 2), 0, "block_all permitted DNS");
    assert_eq!(
        udp("other", peer, 5353, 2),
        2,
        "other container was filtered"
    );
    ctrl.set_egress_mode(EgressMode::AllowAll).unwrap();
    assert_eq!(udp("sb", peer, 5353, 2), 2);
    assert_eq!(
        udp("sb", blocked, 5353, 8),
        0,
        "explicit deny lost to allow_all"
    );
    let mut stream = Stream::new(peer);
    assert!(stream.probe());
    ctrl.set_egress_mode(EgressMode::BlockAll).unwrap();
    assert!(!stream.probe(), "block_all retained established TCP egress");
    assert_eq!(udp("sb", peer, 53, 2), 0, "DNS escaped lockdown");
    ctrl.set_egress_mode(EgressMode::BlockExceptRules).unwrap();
    assert_eq!(
        udp("sb", peer, 5353, 8),
        0,
        "rules_only permitted an unapproved target"
    );
    let target = format!("{peer}/{prefix}").parse().unwrap();
    ctrl.add_allowed_target(&target, 2).unwrap();
    assert_eq!(udp("sb", peer, 5353, 2), 2);
    let mut ttl_stream = Stream::new(peer);
    assert!(ttl_stream.probe());
    ctrl.set_egress_mode(EgressMode::BlockAll).unwrap();
    assert_eq!(
        udp("sb", peer, 5353, 2),
        0,
        "active approval escaped hard lockdown"
    );
    ctrl.set_egress_mode(EgressMode::Ask).unwrap();
    assert_eq!(
        udp("sb", peer, 5353, 2),
        2,
        "mode transition discarded valid approval"
    );
    assert!(
        ttl_stream.probe(),
        "mode transition interrupted approved TCP"
    );
    sleep(Duration::from_secs(3));
    assert!(
        !ttl_stream.probe(),
        "expired approval retained established TCP"
    );
    assert_eq!(
        udp("sb", peer, 5353, 8),
        0,
        "expired target remained reachable"
    );
    ctrl.add_allowed_target(&target, 30).unwrap();
    assert_eq!(udp("sb", peer, 5353, 2), 2);
    let mut revoked_stream = Stream::new(peer);
    assert!(revoked_stream.probe());
    ctrl.remove_allowed_target(&target).unwrap();
    assert!(
        !revoked_stream.probe(),
        "revoked approval retained established TCP"
    );
    assert_eq!(
        udp("sb", peer, 5353, 8),
        0,
        "revoked target remained reachable"
    );
    assert_eq!(
        udp("sb", peer, 53, 2),
        2,
        "DNS resolution unavailable in ask mode"
    );
    ctrl.cleanup().unwrap();
    drop(stream);
    drop(ttl_stream);
    drop(revoked_stream);
    drop(_server);
    for n in [91, 92, 93] {
        run(&["ip", "link", "del", &format!("r{n}")]);
    }
    for name in ["sb", "peer", "other"] {
        run(&["ip", "netns", "del", name]);
    }
}

#[test]
#[ignore = "requires disposable Linux Docker container with CAP_NET_ADMIN and network namespaces"]
fn routed_egress_modes_revoke_flows_and_drop_floods() {
    assert_eq!(
        std::env::var("YUMMI_DISPOSABLE_KERNEL_TEST").as_deref(),
        Ok("1"),
        "run via Dockerfile.kernel-test only"
    );
    exercise(false);
    exercise(true);
}
