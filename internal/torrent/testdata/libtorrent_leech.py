"""Downloads a torrent from one peer with libtorrent and exits 0 once the
data is complete and verified. Usage: libtorrent_leech.py TORRENT SAVE_DIR PORT"""
import sys
import time
import libtorrent as lt

torrent, save, port = sys.argv[1], sys.argv[2], int(sys.argv[3])
ses = lt.session({"listen_interfaces": "127.0.0.1:0", "enable_dht": False, "enable_lsd": False,
                  "enable_upnp": False, "enable_natpmp": False, "allow_multiple_connections_per_ip": True,
                  "alert_mask": lt.alert.category_t.error_notification | lt.alert.category_t.peer_notification
                                | lt.alert.category_t.connect_notification | lt.alert.category_t.status_notification})
h = ses.add_torrent({"ti": lt.torrent_info(torrent), "save_path": save})
h.connect_peer(("127.0.0.1", port))
deadline = time.time() + 60
while time.time() < deadline:
    s = h.status()
    if s.is_seeding:
        print("complete", s.total_done, "v2" if h.torrent_file().info_hashes().has_v2() else "v1")
        sys.exit(0)
    if s.errc.value():
        print("error", s.errc.message())
        sys.exit(1)
    time.sleep(0.2)
for a in ses.pop_alerts():
    print("alert:", a.message())
print("peers:", [(p.ip, p.flags, p.progress) for p in h.get_peer_info()])
print("timeout", h.status().progress)
sys.exit(1)
