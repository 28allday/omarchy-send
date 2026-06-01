"""Nautilus right-click integration for Omarchy-Send.

Adds a "Send via Omarchy-Send" entry to the file/folder context menu. Because
omarchy-send is a terminal app, the entry opens it in a floating presentation
terminal (the same wrapper Omarchy's Transcode entry uses), passing the selected
paths so they arrive pre-staged on the device list — pick a device and send.

Both files and directories are supported (omarchy-send expands a folder on send).

Note on resolution: omarchy-send installs to ~/.local/bin, which is often NOT on
the PATH the Nautilus process (and the terminal it spawns) inherits from the
graphical session. So we resolve it to an absolute path — PATH first, then the
default install locations under $HOME — and invoke it by that absolute path.

Installed to ~/.local/share/nautilus-python/extensions/ by omarchy-send's
install.sh on Omarchy desktops.
"""

import os
import shlex
import shutil

from gi import require_version

require_version("Nautilus", "4.1")

from gi.repository import GObject, Gio, Nautilus


def _resolve(name, fallbacks):
    """Find an executable by PATH, then by a list of absolute fallback paths."""
    found = shutil.which(name)
    if found:
        return found
    for path in fallbacks:
        if path and os.path.isfile(path) and os.access(path, os.X_OK):
            return path
    return None


def _binary():
    home = os.path.expanduser("~")
    fallbacks = []
    bin_dir = os.environ.get("BIN_DIR")
    if bin_dir:
        fallbacks.append(os.path.join(bin_dir, "omarchy-send"))
    fallbacks.append(os.path.join(home, ".local", "bin", "omarchy-send"))
    fallbacks.append(os.path.join(home, "bin", "omarchy-send"))
    return _resolve("omarchy-send", fallbacks)


def _wrapper():
    home = os.path.expanduser("~")
    fallbacks = [
        os.path.join(home, ".local", "share", "omarchy", "bin",
                     "omarchy-launch-floating-terminal-with-presentation"),
    ]
    return _resolve("omarchy-launch-floating-terminal-with-presentation", fallbacks)


class OmarchySendAction(GObject.GObject, Nautilus.MenuProvider):
    def _launch(self, paths):
        wrapper = _wrapper()
        binary = _binary()
        if not wrapper or not binary:
            return
        # Use the absolute binary path: the wrapper's `bash -c` may not have
        # ~/.local/bin on its PATH either.
        cmd = shlex.join([binary, *paths])
        Gio.Subprocess.new([wrapper, cmd], Gio.SubprocessFlags.NONE)

    def _selected_paths(self, files):
        paths = []
        seen = set()
        for file in files:
            location = file.get_location()
            if not location:
                continue
            path = location.get_path()
            if path and path not in seen:
                seen.add(path)
                paths.append(path)
        return paths

    def _make_item(self, paths):
        label = (
            "Send via Omarchy-Send"
            if len(paths) == 1
            else f"Send {len(paths)} items via Omarchy-Send"
        )
        item = Nautilus.MenuItem(
            name="OmarchySendNautilus::send",
            label=label,
            icon="omarchy-send",
        )
        item.connect("activate", self._on_activate, paths)
        return item

    def _on_activate(self, _menu, paths):
        self._launch(paths)

    def get_file_items(self, *args):
        files = args[0] if len(args) == 1 else args[1]
        if not _wrapper() or not _binary():
            return []
        paths = self._selected_paths(files)
        if not paths:
            return []
        return [self._make_item(paths)]
