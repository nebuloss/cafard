#!/usr/bin/env python3
"""Generate the cafard palette for internal/web/assets/static/app.css.

No Material colour tool is available offline, so this is a small, faithful
port of the parts of material-color-utilities that build a scheme:

- CAM16 under the default viewing conditions of material-color-utilities
  (D65, adapting luminance 200/pi * Y(L*=50), background L* 50, average
  surround);
- HCT (hue and chroma from CAM16, tone = CIE L*), solved the way the
  original HCT did it: search J for the wanted L*, and reduce chroma until
  the colour fits in sRGB;
- the "tonal spot" scheme (M3 default): primary chroma 36, secondary 16,
  tertiary hue +60 chroma 24, neutral 6, neutral variant 8, error hue 25
  chroma 84, with the role tones of the 2023 M3 spec.

Seed: chestnut #8b5a2b (a cockroach). Run it and paste the output:

    python3 scripts/palette.py

It also prints the ANSI colours (--ansi-0..15) and the 8 stream tag tones
(--tag-0..7) of each theme, each at the lightest (light theme) or darkest
(dark theme) tone that still reaches a contrast of 4.5:1 against both
--surface and --surface-container, with the measured ratios.
"""

import math

SEED = 0x8B5A2B


# ---- sRGB, XYZ, L* ---------------------------------------------------------

def linearize(c8):
    c = c8 / 255.0
    return (c / 12.92 if c <= 0.040449936 else ((c + 0.055) / 1.055) ** 2.4) * 100.0


def delinearize(c):
    c /= 100.0
    v = c * 12.92 if c <= 0.0031308 else 1.055 * c ** (1 / 2.4) - 0.055
    return max(0, min(255, round(v * 255.0)))


SRGB_TO_XYZ = [
    [0.41233895, 0.35762064, 0.18051042],
    [0.2126, 0.7152, 0.0722],
    [0.01932141, 0.11916382, 0.95034478],
]
XYZ_TO_SRGB = [
    [3.2413774792388685, -1.5376652402851851, -0.49885366846268053],
    [-0.9691452513005321, 1.8758853451067872, 0.04156585616912061],
    [0.05562093689691305, -0.20395524564742123, 1.0571799111220335],
]
WHITE = [95.047, 100.0, 108.883]


def mul(m, v):
    return [sum(m[i][j] * v[j] for j in range(3)) for i in range(3)]


def argb_to_xyz(argb):
    rgb = [linearize((argb >> 16) & 255), linearize((argb >> 8) & 255), linearize(argb & 255)]
    return mul(SRGB_TO_XYZ, rgb)


def xyz_to_argb(xyz):
    r, g, b = (delinearize(c) for c in mul(XYZ_TO_SRGB, xyz))
    return (r << 16) | (g << 8) | b


def lab_f(t):
    e, k = 216 / 24389, 24389 / 27
    return t ** (1 / 3) if t > e else (k * t + 16) / 116


def lab_invf(ft):
    e, k = 216 / 24389, 24389 / 27
    ft3 = ft ** 3
    return ft3 if ft3 > e else (116 * ft - 16) / k


def y_from_lstar(lstar):
    return 100.0 * lab_invf((lstar + 16) / 116)


def lstar_from_y(y):
    return 116 * lab_f(y / 100.0) - 16


def lstar_of(argb):
    return lstar_from_y(argb_to_xyz(argb)[1])


def luminance(argb):
    return argb_to_xyz(argb)[1] / 100.0


def contrast(a, b):
    la, lb = luminance(a), luminance(b)
    hi, lo = max(la, lb), min(la, lb)
    return (hi + 0.05) / (lo + 0.05)


# ---- CAM16 -----------------------------------------------------------------

M16 = [
    [0.401288, 0.650173, -0.051461],
    [-0.250268, 1.204414, 0.045854],
    [-0.002079, 0.048952, 0.953127],
]
M16_INV = [
    [1.8620678, -1.0112547, 0.14918678],
    [0.38752654, 0.62144744, -0.00897398],
    [-0.0158415, -0.03412294, 1.0499644],
]


class VC:
    def __init__(self):
        adapting = (200.0 / math.pi) * y_from_lstar(50.0) / 100.0
        bg_lstar = 50.0
        surround = 2.0
        rgb_w = mul(M16, WHITE)
        f = 0.8 + surround / 10.0
        c = (0.59 + (0.69 - 0.59) * ((f - 0.9) * 10.0)) if f >= 0.9 else (0.525 + (0.59 - 0.525) * ((f - 0.8) * 10.0))
        d = f * (1.0 - (1.0 / 3.6) * math.exp((-adapting - 42.0) / 92.0))
        d = max(0.0, min(1.0, d))
        self.rgb_d = [d * (100.0 / w) + 1.0 - d for w in rgb_w]
        k = 1.0 / (5.0 * adapting + 1.0)
        k4 = k ** 4
        k4f = 1.0 - k4
        self.fl = k4 * adapting + 0.1 * k4f * k4f * (5.0 * adapting) ** (1 / 3)
        self.n = y_from_lstar(bg_lstar) / WHITE[1]
        self.z = 1.48 + math.sqrt(self.n)
        self.nbb = 0.725 / self.n ** 0.2
        self.ncb = self.nbb
        self.c = c
        self.nc = f
        rgb_af = [(self.fl * self.rgb_d[i] * rgb_w[i] / 100.0) ** 0.42 for i in range(3)]
        rgb_a = [400.0 * x / (x + 27.13) for x in rgb_af]
        self.aw = (2.0 * rgb_a[0] + rgb_a[1] + 0.05 * rgb_a[2]) * self.nbb
        self.fl_root = self.fl ** 0.25


V = VC()


def cam_from_argb(argb):
    xyz = argb_to_xyz(argb)
    rgb = mul(M16, xyz)
    rgb_d = [V.rgb_d[i] * rgb[i] for i in range(3)]
    rgb_af = [(V.fl * abs(x) / 100.0) ** 0.42 for x in rgb_d]
    rgb_a = [math.copysign(1, rgb_d[i]) * 400.0 * rgb_af[i] / (rgb_af[i] + 27.13) for i in range(3)]
    ra, ga, ba = rgb_a
    a = (11.0 * ra - 12.0 * ga + ba) / 11.0
    b = (ra + ga - 2.0 * ba) / 9.0
    u = (20.0 * ra + 20.0 * ga + 21.0 * ba) / 20.0
    p2 = (40.0 * ra + 20.0 * ga + ba) / 20.0
    hue = math.degrees(math.atan2(b, a)) % 360.0
    ac = p2 * V.nbb
    j = 100.0 * (ac / V.aw) ** (V.c * V.z)
    hue_prime = hue + 360 if hue < 20.14 else hue
    e_hue = 0.25 * (math.cos(math.radians(hue_prime) + 2.0) + 3.8)
    p1 = 50000.0 / 13.0 * e_hue * V.nc * V.ncb
    t = p1 * math.hypot(a, b) / (u + 0.305)
    alpha = (1.64 - 0.29 ** V.n) ** 0.73 * t ** 0.9
    chroma = alpha * math.sqrt(j / 100.0)
    m = chroma * V.fl_root
    s = 50.0 * math.sqrt((alpha * V.c) / (V.aw + 4.0))
    jstar = (1.0 + 100.0 * 0.007) * j / (1.0 + 0.007 * j)
    mstar = 1.0 / 0.0228 * math.log1p(0.0228 * m)
    return dict(hue=hue, chroma=chroma, j=j, jstar=jstar, astar=mstar * math.cos(math.radians(hue)),
                bstar=mstar * math.sin(math.radians(hue)))


def argb_from_jch(j, c, h):
    alpha = 0.0 if (c == 0.0 or j == 0.0) else c / math.sqrt(j / 100.0)
    t = (alpha / (1.64 - 0.29 ** V.n) ** 0.73) ** (1.0 / 0.9)
    h_rad = math.radians(h)
    e_hue = 0.25 * (math.cos(h_rad + 2.0) + 3.8)
    ac = V.aw * (j / 100.0) ** (1.0 / V.c / V.z)
    p1 = e_hue * (50000.0 / 13.0) * V.nc * V.ncb
    p2 = ac / V.nbb
    hs, hc = math.sin(h_rad), math.cos(h_rad)
    gamma = 23.0 * (p2 + 0.305) * t / (23.0 * p1 + 11.0 * t * hc + 108.0 * t * hs)
    a, b = gamma * hc, gamma * hs
    ra = (460.0 * p2 + 451.0 * a + 288.0 * b) / 1403.0
    ga = (460.0 * p2 - 891.0 * a - 261.0 * b) / 1403.0
    ba = (460.0 * p2 - 220.0 * a - 6300.0 * b) / 1403.0

    def un(x):
        base = max(0, (27.13 * abs(x)) / (400.0 - abs(x)))
        return math.copysign(1, x) * (100.0 / V.fl) * base ** (1.0 / 0.42)

    rgb_c = [un(ra), un(ga), un(ba)]
    rgb = [rgb_c[i] / V.rgb_d[i] for i in range(3)]
    return xyz_to_argb(mul(M16_INV, rgb))


def cam_distance(c1, c2):
    dj = c1["jstar"] - c2["jstar"]
    da = c1["astar"] - c2["astar"]
    db = c1["bstar"] - c2["bstar"]
    return 1.41 * math.sqrt(dj * dj + da * da + db * db) ** 0.63


def gray(lstar):
    y = y_from_lstar(lstar)
    c = delinearize(y)
    return (c << 16) | (c << 8) | c


def find_by_j(hue, chroma, lstar):
    low, high = 0.0, 100.0
    best_dl, best_de, best = 1000.0, 1000.0, None
    while abs(low - high) > 0.01:
        mid = low + (high - low) / 2
        clipped = argb_from_jch(mid, chroma, hue)
        clipped_l = lstar_of(clipped)
        dl = abs(lstar - clipped_l)
        if dl < 0.2:
            cam = cam_from_argb(clipped)
            ref = cam_from_argb(argb_from_jch(cam["j"], cam["chroma"], hue))
            de = cam_distance(cam, ref)
            if de <= 1.0 and de <= best_de:
                best_dl, best_de, best = dl, de, clipped
        if best_dl == 0 and best_de == 0:
            break
        if clipped_l < lstar:
            low = mid
        else:
            high = mid
    return best


def hct(hue, chroma, tone):
    if chroma < 1.0 or round(tone) <= 0.0 or round(tone) >= 100.0:
        return gray(tone)
    hue %= 360.0
    high, mid, low = chroma, chroma, 0.0
    first, answer = True, None
    while abs(low - high) >= 0.4:
        possible = find_by_j(hue, mid, tone)
        if first:
            if possible is not None:
                return possible
            first = False
            mid = low + (high - low) / 2
            continue
        if possible is None:
            high = mid
        else:
            answer, low = possible, mid
        mid = low + (high - low) / 2
    return answer if answer is not None else gray(tone)


def hexs(argb):
    return "#%06x" % argb


# ---- scheme ----------------------------------------------------------------

seed = cam_from_argb(SEED)
H = seed["hue"]
P = lambda t: hct(H, 36, t)
S = lambda t: hct(H, 16, t)
T = lambda t: hct(H + 60, 24, t)
N = lambda t: hct(H, 6, t)
NV = lambda t: hct(H, 8, t)
E = lambda t: hct(25, 84, t)
OK = lambda t: hct(150, 36, t)  # success: a green, not an M3 role

LIGHT = [
    ("primary", P(40)), ("on-primary", P(100)), ("primary-container", P(90)), ("on-primary-container", P(30)),
    ("secondary", S(40)), ("on-secondary", S(100)), ("secondary-container", S(90)), ("on-secondary-container", S(30)),
    ("tertiary", T(40)), ("on-tertiary", T(100)), ("tertiary-container", T(90)), ("on-tertiary-container", T(30)),
    ("error", E(40)), ("on-error", E(100)), ("error-container", E(90)), ("on-error-container", E(30)),
    ("success", OK(40)), ("success-container", OK(90)), ("on-success-container", OK(30)),
    ("surface", N(98)), ("on-surface", N(10)), ("surface-variant", NV(90)), ("on-surface-variant", NV(30)),
    ("surface-container-lowest", N(100)), ("surface-container-low", N(96)), ("surface-container", N(94)),
    ("surface-container-high", N(92)), ("surface-container-highest", N(90)),
    ("outline", NV(50)), ("outline-variant", NV(80)), ("inverse-surface", N(20)), ("inverse-on-surface", N(95)),
    ("inverse-primary", P(80)),
]
DARK = [
    ("primary", P(80)), ("on-primary", P(20)), ("primary-container", P(30)), ("on-primary-container", P(90)),
    ("secondary", S(80)), ("on-secondary", S(20)), ("secondary-container", S(30)), ("on-secondary-container", S(90)),
    ("tertiary", T(80)), ("on-tertiary", T(20)), ("tertiary-container", T(30)), ("on-tertiary-container", T(90)),
    ("error", E(80)), ("on-error", E(20)), ("error-container", E(30)), ("on-error-container", E(90)),
    ("success", OK(80)), ("success-container", OK(30)), ("on-success-container", OK(90)),
    ("surface", N(6)), ("on-surface", N(90)), ("surface-variant", NV(30)), ("on-surface-variant", NV(80)),
    ("surface-container-lowest", N(4)), ("surface-container-low", N(10)), ("surface-container", N(12)),
    ("surface-container-high", N(17)), ("surface-container-highest", N(22)),
    ("outline", NV(60)), ("outline-variant", NV(30)), ("inverse-surface", N(90)), ("inverse-on-surface", N(20)),
    ("inverse-primary", P(40)),
]

# ANSI: black, red, green, yellow, blue, magenta, cyan, white; then bright.
ANSI_HUES = [None, 25, 145, 95, 270, 330, 205, None]


def readable(hue, chroma, start, step, bgs, target=4.5):
    """The tone nearest to start, walking by step, that reaches target on every bg."""
    t = start
    while 0 < t < 100:
        c = hct(hue, chroma, t) if hue is not None else hct(H, chroma, t)
        if all(contrast(c, bg) >= target for bg in bgs):
            return c
        t += step
    raise SystemExit("no readable tone")


def ansi(theme, bgs):
    out = []
    light = theme == "light"
    for bright in (False, True):
        for i, hue in enumerate(ANSI_HUES):
            if hue is None:
                # black and white are neutrals; in light mode "white" must
                # still be readable on the light surface, so it is a grey.
                black = i == 0
                if light:
                    start = (25 if bright else 10) if black else (38 if bright else 45)
                else:
                    start = (55 if black else 88) + (10 if bright else 0)
                c = readable(None, 4, start, -1 if light else 1, bgs) if (light or black) else hct(H, 4, start)
            else:
                start = 50 if light else 62
                if bright:
                    start = 42 if light else 78
                c = readable(hue, 70, start, -1 if light else 1, bgs)
            out.append(c)
    return out


TAG_HUES = [25, 70, 115, 160, 205, 250, 295, 340]


def tags(theme, bgs):
    light = theme == "light"
    return [readable(h, 48, 50 if light else 65, -1 if light else 1, bgs) for h in TAG_HUES]


def block(name, roles):
    print(f"/* {name} */")
    for k, v in roles:
        print(f"  --{k}: {hexs(v)};")
    d = dict(roles)
    bgs = [d["surface"], d["surface-container"]]
    cols = ansi(name, bgs)
    print("  /* ANSI, contrast against --surface / --surface-container */")
    for i, c in enumerate(cols):
        print(f"  --ansi-{i}: {hexs(c)}; /* {contrast(c, bgs[0]):.2f} / {contrast(c, bgs[1]):.2f} */")
    print("  /* stream tags, same rule */")
    for i, c in enumerate(tags(name, bgs)):
        print(f"  --tag-{i}: {hexs(c)}; /* {contrast(c, bgs[0]):.2f} / {contrast(c, bgs[1]):.2f} */")


print(f"/* seed {hexs(SEED)}: CAM16 hue {seed['hue']:.1f}, chroma {seed['chroma']:.1f}, tone {lstar_of(SEED):.1f} */")
block("light", LIGHT)
block("dark", DARK)
