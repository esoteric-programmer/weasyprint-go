b=base(); rgb=b.convert('RGB'); L=b.convert('L'); one=b.convert('1'); P=rgb.quantize(50)
gv=list(L.tobytes()); rv=list(rgb.tobytes()); av=list(b.tobytes())
# ICNS (PNG chunks via Pillow)
big=b.resize((128,128)); save('a.icns', big, 'ICNS')
# ICNS with RLE it32 + mask, built by hand
def rle_band(vals):
    out=bytearray(); i=0
    while i<len(vals):
        j=i
        while j<len(vals) and vals[j]==vals[i] and j-i<130: j+=1
        if j-i>=3: out+=bytes([0x80+(j-i)-3+0, vals[i]]) if False else bytes([(j-i)+125, vals[i]]); i=j
        else:
            k=i; lit=[]
            while k<len(vals) and len(lit)<128 and not (k+2<len(vals) and vals[k]==vals[k+1]==vals[k+2]): lit.append(vals[k]); k+=1
            out+=bytes([len(lit)-1])+bytes(lit); i=k
    return bytes(out)
im32=b.resize((32,32)); px=list(im32.getdata())
il32=b''.join(rle_band([p[c] for p in px]) for c in range(3)); l8mk=bytes(p[3] for p in px)
body=b'il32'+struct.pack('>I',8+len(il32))+il32+b'l8mk'+struct.pack('>I',8+len(l8mk))+l8mk
raw('rle.icns', b'icns'+struct.pack('>I',8+len(body))+body)
# IM
for m,n in [(rgb,'rgb'),(L,'l'),(b,'rgba'),(one,'one'),(P,'p'),(b.convert('LA'),'la'),(b.convert('CMYK'),'cmyk')]:
    save(n+'.im', m, 'IM')
# MSP (DanM via Pillow) and LinS by hand
save('a.msp', one, 'MSP')
def lins():
    stride=(W+7)//8; rows=[]
    ob=one.tobytes()
    for y in range(H):
        r=ob[y*stride:(y+1)*stride]; enc=bytearray(); i=0
        while i<len(r):
            j=i
            while j<len(r) and r[j]==r[i] and j-i<255: j+=1
            if j-i>=3: enc+=bytes([0,j-i,r[i]]); i=j
            else: enc+=bytes([1,r[i]]); i+=1
        rows.append(bytes(enc))
    hdr=bytearray(32); hdr[0:4]=b'LinS'; struct.pack_into('<HH',hdr,4,W,H); struct.pack_into('<HH',hdr,8,1,1)
    cs=0
    for i in range(0,30,2): cs^=struct.unpack_from('<H',hdr,i)[0]
    struct.pack_into('<H',hdr,30,cs)
    return bytes(hdr)+b''.join(struct.pack('<H',len(r)) for r in rows)+b''.join(rows)
raw('lins.msp', lins())
# SPIDER (float): Pillow F; reference normalized like ours
def norm(im):
    import numpy as np
    a=np.asarray(im,dtype='float64'); lo,hi=a.min(),a.max()
    return Image.fromarray(np.clip(np.round((a-lo)*255/(hi-lo)),0,255).astype('uint8'),'L')
save('a.spider', L.convert('F').point(lambda v: v*3.5-100), 'SPIDER', adjust=norm)
# BLP
save('p.blp', P, 'BLP')
save('blp1.blp', P, 'BLP', blp_version='BLP1')
# DDS via Pillow
save('rgb.dds', rgb, 'DDS'); save('rgba.dds', b, 'DDS'); save('l.dds', L, 'DDS'); save('la.dds', b.convert('LA'), 'DDS')
for pf in ('DXT1','DXT3','DXT5','BC5'):
    try: save(pf.lower()+'.dds', b if pf!='BC5' else rgb, 'DDS', pixel_format=pf)
    except Exception as e: print('dds',pf,e)
def dds(fourcc, data, w=W, h=H, dx10=None):
    hdr=bytearray(128); hdr[0:4]=b'DDS '; struct.pack_into('<I',hdr,4,124)
    struct.pack_into('<III',hdr,8,0x1007,h,w); struct.pack_into('<I',hdr,76,32); struct.pack_into('<I',hdr,80,4); hdr[84:88]=fourcc
    ext=struct.pack('<IIIII',dx10,3,0,1,0) if dx10 is not None else b''
    return bytes(hdr)+ext+data
nb=((W+3)//4)*((H+3)//4)
rnd=bytes(random.getrandbits(8) for _ in range(nb*16))
raw('bc7.dds', dds(b'DX10', rnd, dx10=98)); raw('bc6h.dds', dds(b'DX10', rnd, dx10=95)); raw('bc6hs.dds', dds(b'DX10', rnd, dx10=96))
raw('bc4.dds', dds(b'ATI1', rnd[:nb*8])); raw('bc5s.dds', dds(b'BC5S', rnd)); raw('ati2.dds', dds(b'ATI2', rnd))
raw('dxt1r.dds', dds(b'DXT1', rnd[:nb*8])); raw('dxt5r.dds', dds(b'DXT5', rnd))
# FTEX
def ftex(fmt, data):
    return b'FTEX'+struct.pack('<iiiiiii',0,W,H,1,1,fmt,32)+struct.pack('<i',len(data))+data
raw('rgb.ftu', ftex(1, rgb.tobytes())); raw('dxt1.ftc', ftex(0, rnd[:nb*8]))
# PIXAR
ph=bytearray(1024); ph[0:4]=b'\x80\xe8\x00\x00'; struct.pack_into('<HH',ph,416,H,W); struct.pack_into('<HH',ph,424,14,2)
raw('a.pxr', bytes(ph)+rgb.tobytes())
# XV thumbnail
raw('a.xv', b'P7 332\n#XVVERSION:Version 2.28\n#END_OF_COMMENTS\n%d %d 255\n'%(W,H)+bytes((x*13)%256 for x in range(W*H)))
# McIdas (8-bit)
w=[0]*65; w[2]=4; w[9]=H; w[10]=W; w[11]=1; w[14]=1; w[15]=0; w[34]=256
raw('a.mcidas', b'\0\0\0\0'+struct.pack('!64i',*w[1:])[4:] if False else struct.pack('!64i',*w[1:])+bytes(L.tobytes()))
# FITS 8-bit and 16-bit
def fits(bitpix, data, extra=()):
    cards=['SIMPLE  =                    T','BITPIX  = %20d'%bitpix,'NAXIS   =                    2','NAXIS1  = %20d'%W,'NAXIS2  = %20d'%H]+list(extra)+['END']
    h=''.join(c.ljust(80) for c in cards).encode(); h+=b' '*((-len(h))%2880)
    return h+data+b'\0'*((-len(data))%2880)
flip=L.transpose(Image.FLIP_TOP_BOTTOM)
raw('a8.fits', fits(8, flip.tobytes()))
v16=[ (v*100-3000) for v in flip.tobytes()]
raw('a16.fits', fits(16, b''.join(struct.pack('>h',v) for v in v16)), adjust=lambda im: norm(L))
# GBR v2 gray and RGBA
def gbr(depth, data):
    name=b'brush\0'; hs=28+len(name)
    return struct.pack('>IIIII',hs,2,W,H,depth)+b'GIMP'+struct.pack('>I',10)+name+data
raw('l.gbr', gbr(1, L.tobytes())); raw('rgba.gbr', gbr(4, b.tobytes()))
# IMT
raw('a.imt', b'width %d\nheight %d\npixel n8\n\x0c'%(W,H)+L.tobytes())
# PSD (raw and packbits), RGB+alpha, gray, indexed, CMYK, Lab
def psd(mode, channels, planes, comp=0, cm=b'', bits=8):
    hdr=b'8BPS'+struct.pack('>H',1)+b'\0'*6+struct.pack('>HIIHH',channels,H,W,bits,mode)
    hdr+=struct.pack('>I',len(cm))+cm+struct.pack('>I',0)+struct.pack('>I',0)
    if comp==0: return hdr+struct.pack('>H',0)+b''.join(planes)
    counts=b''; data=b''
    for pl in planes:
        stride=len(pl)//H
        for y in range(H):
            row=pl[y*stride:(y+1)*stride]; enc=bytearray(); i=0
            while i<len(row):
                j=i
                while j<len(row) and row[j]==row[i] and j-i<128: j+=1
                if j-i>=2: enc+=bytes([257-(j-i)&255, row[i]]); i=j
                else:
                    k=i
                    while k<len(row) and k-i<128 and not (k+1<len(row) and row[k]==row[k+1]): k+=1
                    enc+=bytes([k-i-1])+row[i:k]; i=k
            counts+=struct.pack('>H',len(enc)); data+=bytes(enc)
    return hdr+struct.pack('>H',1)+counts+data
chan=lambda im,i: im.getchannel(i).tobytes()
raw('rgba.psd', psd(3,4,[chan(b,i) for i in range(4)]))
raw('rgb_rle.psd', psd(3,3,[chan(rgb,i) for i in range(3)],comp=1))
raw('l.psd', psd(1,1,[L.tobytes()],comp=1))
pal=P.getpalette()[:768]; pal+= [0]*(768-len(pal))
raw('p.psd', psd(2,1,[P.tobytes()], cm=bytes(pal[0::3]+pal[1::3]+pal[2::3])))
cm=b.convert('CMYK'); raw('cmyk.psd', psd(4,4,[bytes(255-x for x in chan(cm,i)) for i in range(4)]))
lab=rgb.convert('LAB'); raw('lab.psd', psd(9,3,[chan(lab,i) for i in range(3)]), adjust=lambda im: im.convert('RGB') if False else im)
# FLI: one frame, COLOR_64 + BRUN
def fli():
    pal=b''.join(bytes(((i*3)%64,(i*5)%64,(i*7)%64)) for i in range(256))
    color=struct.pack('<H',1)+b'\x00\x00'+pal; ch1=struct.pack('<IH',6+len(color),11)+color
    brun=b''
    idx=P.tobytes()
    for y in range(H):
        row=idx[y*W:(y+1)*W]; line=b'\x00'; i=0
        while i<len(row):
            j=i
            while j<len(row) and row[j]==row[i] and j-i<127: j+=1
            if j-i>=2: line+=bytes([j-i,row[i]]); i=j
            else: line+=bytes([256-1,row[i]]); i+=1
        brun+=line
    ch2=struct.pack('<IH',6+len(brun),15)+brun
    frame=struct.pack('<IHH',16+len(ch1)+len(ch2),0xF1FA,2)+b'\0'*8+ch1+ch2
    hdr=bytearray(128); struct.pack_into('<IHHHHHHI',hdr,0,128+len(frame),0xAF11,1,W,H,8,0,5)
    return bytes(hdr)+frame
raw('a.fli', fli())
# IPTC raw grayscale
def rec(r,t,data): return bytes([0x1c,r,t])+struct.pack('>H',len(data))+data
iptc=rec(3,60,b'\x01\x00')+rec(3,20,struct.pack('>H',W))+rec(3,30,struct.pack('>H',H))+rec(3,120,b'\x01')+rec(8,10,L.tobytes())
raw('a.iim', iptc)
# PhotoCD base image: reference = Pillow's YCC values converted with the PhotoYCC formulas
pcd=bytearray(96*2048+768*512*3//2); pcd[2048:2052]=b'PCD_'; pcd[2048+1538]=0
for y in range(0,512,2):
    o=96*2048+y//2*3*768
    for x in range(768):
        pcd[o+x]=(x+y)%256; pcd[o+768+x]=(x*2+y)%256
    for x in range(384):
        pcd[o+1536+x]=(x+100)%256; pcd[o+1536+384+x]=(200-x)%256
def ycc(im):
    import numpy as np
    a=np.asarray(im.convert('RGB'),dtype='float64'); l=1.3584*a[...,0]; c1=2.2179*(a[...,1]-156); c2=1.8215*(a[...,2]-137)
    out=np.stack([l+c2, l-0.194*c1-0.509*c2, l+c1],-1)
    return Image.fromarray(np.clip(np.round(out),0,255).astype('uint8'),'RGB')
raw('a.pcd', bytes(pcd))
