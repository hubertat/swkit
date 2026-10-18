# Stand-in for the Presto's frozen psram module: the "RAM disk" is a plain
# directory, so the simulator exercises the runtime-save / boot-commit path.
import os


def mkramfs(size=64 * 1024, mount_point="/ramfs", debug=False):
    try:
        os.mkdir(mount_point)
    except OSError:
        pass
