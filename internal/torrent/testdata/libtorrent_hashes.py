"""Prints libtorrent's info hashes for a directory, for comparison with
weightkeep's builder. Usage: libtorrent_hashes.py DIR PIECE_LENGTH"""
import sys
import libtorrent as lt

root, piece = sys.argv[1], int(sys.argv[2])
fs = lt.file_storage()
lt.add_files(fs, root)
t = lt.create_torrent(fs, piece)  # hybrid v1+v2 by default in libtorrent 2
lt.set_piece_hashes(t, root.rsplit("/", 1)[0])
ti = lt.torrent_info(t.generate())
ih = ti.info_hashes()
print(ih.v1, ih.v2)
