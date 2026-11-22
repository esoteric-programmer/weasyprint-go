"""Generate test images and Pillow reference digests (ref.txt)."""
import io, os, struct, hashlib, sys, random
from PIL import Image
D='/tmp/imgt'
for f in os.listdir(D): os.remove(os.path.join(D,f))
random.seed(3)
W,H=37,23
def base(mode='RGBA'):
    im=Image.new('RGBA',(W,H))
    px=im.load()
    for y in range(H):
        for x in range(W):
            px[x,y]=((x*7)%256,(y*11)%256,((x+y)*5)%256, 255 if (x+y)%5 else (x*13)%256)
    return im if mode=='RGBA' else im.convert(mode)
refs={}
def ref_of(path, adjust=None):
    im=Image.open(path)
    im.load()
    if adjust: im=adjust(im)
    if 'transparency' in im.info and im.mode in ('P','L','RGB'):
        im=im.convert('RGBA')
    rgba=im.convert('RGBA')
    return f'{im.width}x{im.height} '+hashlib.md5(rgba.tobytes()).hexdigest()
def save(name, im, fmt, adjust=None, **kw):
    p=os.path.join(D,name); im.save(p, fmt, **kw); refs[name]=ref_of(p, adjust)
def raw(name, data, adjust=None):
    p=os.path.join(D,name); open(p,'wb').write(data)
    try: refs[name]=ref_of(p, adjust)
    except Exception as e: refs[name]='PILLOW-ERROR '+type(e).__name__+' '+str(e)[:60]
exec(open(os.path.join(os.path.dirname(__file__),"imgt_cases.py")).read()); exec(open(os.path.join(os.path.dirname(__file__),"imgt_cases2.py")).read())
with open(os.path.join(D,'ref.txt'),'w') as f:
    for k in sorted(refs): f.write(f'{k} {refs[k]}\n')
print(len(refs),'files')
