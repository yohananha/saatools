"""
make_icons.py — Generate all Android mipmap icon sizes from saatool-icon.png
Run from the android/ directory:  python make_icons.py
"""
from PIL import Image
import os, sys, shutil

SRC = os.path.join(os.path.dirname(__file__), "saatool-icon.png")
RES = os.path.join(os.path.dirname(__file__), "app", "src", "main", "res")

# Standard launcher icon sizes (dp × density)
LAUNCHER = {
    "mipmap-mdpi":    48,
    "mipmap-hdpi":    72,
    "mipmap-xhdpi":   96,
    "mipmap-xxhdpi":  144,
    "mipmap-xxxhdpi": 192,
}

# Adaptive foreground sizes (108dp × density — full icon used as foreground layer)
ADAPTIVE = {
    "mipmap-mdpi":    108,
    "mipmap-hdpi":    162,
    "mipmap-xhdpi":   216,
    "mipmap-xxhdpi":  324,
    "mipmap-xxxhdpi": 432,
}

def resize(src_img, size, dest_path):
    img = src_img.resize((size, size), Image.LANCZOS)
    os.makedirs(os.path.dirname(dest_path), exist_ok=True)
    img.save(dest_path, "PNG")
    print(f"  {size}x{size}  ->  {dest_path}")

def main():
    if not os.path.exists(SRC):
        print(f"ERROR: Source image not found: {SRC}")
        print("Save the logo as  android/saatool-icon.png  and re-run.")
        sys.exit(1)

    src = Image.open(SRC).convert("RGBA")
    print(f"Loaded {SRC}  ({src.width}x{src.height})")

    print("\n-- Launcher icons (ic_launcher.png) --")
    for folder, size in LAUNCHER.items():
        resize(src, size, os.path.join(RES, folder, "ic_launcher.png"))
        # Round icon = same image (rounded shape applied by OS)
        resize(src, size, os.path.join(RES, folder, "ic_launcher_round.png"))

    print("\n-- Adaptive foreground (ic_launcher_foreground.png) --")
    for folder, size in ADAPTIVE.items():
        resize(src, size, os.path.join(RES, folder, "ic_launcher_foreground.png"))

    print("\nDone! All icons generated.")

if __name__ == "__main__":
    main()
