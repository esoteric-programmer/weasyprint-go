b=base()
rgb=b.convert('RGB'); L=b.convert('L'); P=b.convert('RGB').quantize(50); one=b.convert('1')
# QOI
save('a.qoi', rgb, 'QOI'); save('b.qoi', b, 'QOI')
# PNM
save('a.ppm', rgb, 'PPM'); save('a.pgm', L, 'PPM'); save('a.pbm', one, 'PPM')
def plain(magic, maxval, vals, w=W, h=H, comments=True):
    s=f'{magic}\n# comment\n{w} {h}\n' + (f'{maxval}\n' if maxval else '')
    s+='\n'.join(' '.join(str(v) for v in vals[i:i+17]) for i in range(0,len(vals),17))+'\n'
    return s.encode()
gv=list(L.tobytes()); rv=list(rgb.tobytes())
raw('p2.pgm', plain('P2',255,gv)); raw('p3.ppm', plain('P3',255,rv))
raw('p2_100.pgm', plain('P2',100,[v*100//255 for v in gv]))
raw('p1.pbm', ('P1\n%d %d\n'%(W,H)+''.join('1' if v<128 else '0' for v in gv)).encode())
raw('p6_1000.ppm', b'P6 %d %d 1000\n'%(W,H)+b''.join(struct.pack('>H',v*1000//255) for v in rv))
# 16-bit gray: Pillow keeps 16 bits; reference scaled to 8 bits
d16=[(v*257) for v in gv]
raw('p5_16.pgm', b'P5\n%d %d\n65535\n'%(W,H)+b''.join(struct.pack('>H',v) for v in d16), adjust=lambda im: im.point(lambda v: v/257).convert('L'))
raw('pf.pfm', b'Pf\n%d %d\n-1.0\n'%(W,H)+b''.join(struct.pack('<f',v/255) for v in gv), adjust=lambda im: im.point(lambda v: v*255).convert('L'))
# XBM
save('a.xbm', one, 'XBM')
# XPM
pal=['#%02x%02x%02x'%(x*40%256,x*90%256,x*17%256) for x in range(6)]
chars='.#ab+ '
rows=[''.join(chars[(x*y+x)%6] for x in range(W)) for y in range(H)]
xpm='/* XPM */\nstatic char *a[] = {\n"%d %d 7 1",\n'%(W,H)+''.join(f'"{chars[i]} c {pal[i]}",\n' for i in range(5))+'"  c None",\n"* c #000000",\n'+',\n'.join(f'"{r}"' for r in rows)+'};\n'
raw('a.xpm', xpm.encode())
# PCX
save('rgb.pcx', rgb, 'PCX'); save('l.pcx', L, 'PCX'); save('p.pcx', P, 'PCX'); save('one.pcx', one, 'PCX')
# DCX (one PCX page)
pcx=open(D+'/p.pcx','rb').read()
raw('a.dcx', struct.pack('<II', 0x3ADE68B1, 12)+struct.pack('<I',0)+pcx)
# 4-plane EGA PCX
def pcx16():
    hdr=bytearray(128); hdr[0]=10; hdr[1]=5; hdr[2]=1; hdr[3]=1
    struct.pack_into('<HHHH',hdr,4,0,0,W-1,H-1)
    ega=[(i*16,255-i*16,(i*37)%256) for i in range(16)]
    for i,c in enumerate(ega): hdr[16+3*i:19+3*i]=bytes(c)
    hdr[65]=4; stride=(W+7)//8; stride+=stride%2; struct.pack_into('<H',hdr,66,stride)
    body=bytearray()
    for y in range(H):
        for pl in range(4):
            row=bytearray(stride)
            for x in range(W):
                idx=(x+y)%16
                if idx>>pl&1: row[x//8]|=0x80>>(x%8)
            for byte in row:
                if byte>=0xc0: body+=bytes([0xc1,byte])
                else: body.append(byte)
    return bytes(hdr)+bytes(body)
raw('ega.pcx', pcx16())
# SGI
save('rgb.sgi', rgb, 'SGI'); save('rgba.sgi', b, 'SGI'); save('l.sgi', L, 'SGI')
save('rle.sgi', b, 'SGI', rle=True); save('rlel.sgi', L, 'SGI', rle=True)
# 16-bit SGI uncompressed, built by hand
def sgi16(rle=False):
    hdr=bytearray(512); struct.pack_into('>hbbHHHH',hdr,0,474,1 if rle else 0,2,3,W,H,3)
    planes=[[rv[3*(y*W+x)+c]*256+((x*31)&255) for y in range(H-1,-1,-1) for x in range(W)] for c in range(3)]
    if not rle:
        return bytes(hdr)+b''.join(struct.pack('>H',v) for pl in planes for v in pl)
    rows=[]; 
    for c in range(3):
        for y in range(H):
            vals=planes[c][y*W:(y+1)*W]
            out=b''
            for i in range(0,len(vals),100):
                chunk=vals[i:i+100]
                out+=struct.pack('>H',0x80|len(chunk))+b''.join(struct.pack('>H',v) for v in chunk)
            out+=struct.pack('>H',0)
            rows.append(out)
    tab=512+8*len(rows); starts=[];off=tab
    for r in rows: starts.append(off); off+=len(r)
    return bytes(hdr)+b''.join(struct.pack('>I',s) for s in starts)+b''.join(struct.pack('>I',len(r)) for r in rows)+b''.join(rows)
raw('rgb16.sgi', sgi16()); raw('rgb16rle.sgi', sgi16(True))
# Sun raster
def sun(depth, ftype=1, palette=None, data=None, rle=False):
    stride=((W*depth+15)//16)*2
    pal=b''
    if palette: pal=bytes(c[0] for c in palette)+bytes(c[1] for c in palette)+bytes(c[2] for c in palette)
    if depth==8: rows=[bytes(gv[y*W:(y+1)*W])+bytes(stride-W) for y in range(H)]
    elif depth==24: rows=[b''.join(bytes((rv[3*i+2],rv[3*i+1],rv[3*i])) if ftype!=3 else bytes(rv[3*i:3*i+3]) for i in range(y*W,(y+1)*W))+bytes(stride-3*W) for y in range(H)]
    elif depth==32: rows=[b''.join(bytes((0,rv[3*i+2],rv[3*i+1],rv[3*i])) for i in range(y*W,(y+1)*W)) for y in range(H)]
    elif depth==1:
        rows=[]
        for y in range(H):
            r=bytearray(stride)
            for x in range(W):
                if gv[y*W+x]<128: r[x//8]|=0x80>>(x%8)
            rows.append(bytes(r))
    body=b''.join(rows)
    if rle:
        body=b''.join(bytes(r[:(W*depth+7)//8]) for r in rows)
        enc=bytearray(); i=0
        while i<len(body):
            j=i
            while j<len(body) and body[j]==body[i] and j-i<255: j+=1
            n=j-i
            if n>2 or body[i]==0x80: enc+=bytes([0x80,n-1,body[i]]) if not (n==1 and body[i]==0x80) else b'\x80\x00'; i=j
            else: enc.append(body[i]); i+=1
        body=bytes(enc)
    hdr=struct.pack('>8I',0x59A66A95,W,H,depth,len(body),2 if rle else ftype,1 if palette else 0,len(pal))
    return hdr+pal+body
raw('g8.ras', sun(8)); raw('p8.ras', sun(8, palette=[(i,255-i,(i*3)%256) for i in range(256)]))
raw('rgb24.ras', sun(24)); raw('rgb24t3.ras', sun(24,3)); raw('rgb32.ras', sun(32)); raw('one.ras', sun(1))
raw('rle8.ras', sun(8, rle=True)); raw('rle24.ras', sun(24, rle=True))
# TGA
save('rgb.tga', rgb, 'TGA'); save('rgba.tga', b, 'TGA'); save('l.tga', L, 'TGA'); save('p.tga', P, 'TGA')
save('rgb_rle.tga', rgb, 'TGA', compression='tga_rle'); save('rgba_rle.tga', b, 'TGA', compression='tga_rle'); save('p_rle.tga', P, 'TGA', compression='tga_rle')
save('la.tga', b.convert('LA'), 'TGA'); save('top.tga', rgb, 'TGA', orientation=1)
def tga16(alpha_bit_mode=True):
    hdr=struct.pack('<BBBHHBHHHHBB',0,0,2,0,0,0,0,0,W,H,16,0x20)
    px=b''
    for i in range(W*H):
        r,g,bb=rv[3*i:3*i+3]
        v=((r>>3)<<10)|((g>>3)<<5)|(bb>>3)|(0x8000 if i%3 else 0)
        px+=struct.pack('<H',v)
    return hdr+px
raw('rgb16.tga', tga16())
# ICO / CUR
save('a.ico', b, 'ICO', sizes=[(16,16),(32,32),(37,23)])
im8=b.convert('RGB').resize((32,32)); 
def ico_bmp(img, bpp):
    # build a classic BMP-based icon entry
    w,h=img.size
    bio=io.BytesIO()
    if bpp==32: img.convert('RGBA').save(bio,'BMP')
    elif bpp==8: img.convert('RGB').quantize(200).save(bio,'BMP')
    else: img.convert('RGB').save(bio,'BMP')
    bmp=bio.getvalue()[14:]
    hs=struct.unpack('<I',bmp[:4])[0]
    dib=bytearray(bmp); struct.pack_into('<i',dib,8,h*2)
    if bpp==32 and hs>40:
        # keep a plain 40-byte header with BI_RGB
        pix=bytes(dib[hs:]); dib=bytearray(dib[:40]); struct.pack_into('<I',dib,16,0); dib+=pix
    mstride=((w+31)//32)*4
    mask=bytearray(mstride*h)
    for y in range(h):
        for x in range(w):
            if (x*y)%7==0: mask[(h-1-y)*mstride+x//8]|=0x80>>(x%8)
    dib=bytes(dib)+bytes(mask)
    return dib
def ico(entries, kind=1):
    hdr=struct.pack('<HHH',0,kind,len(entries)); off=6+16*len(entries); dirs=b''; datas=b''
    for (w,h,bpp,data) in entries:
        dirs+=struct.pack('<BBBBHHII',w%256,h%256,0,0,1,bpp,len(data),off); off+=len(data); datas+=data
    return hdr+dirs+datas
raw('bmp24.ico', ico([(32,32,24,ico_bmp(im8,24))]))
raw('bmp8.ico', ico([(32,32,8,ico_bmp(im8,8))]))
raw('bmp32.ico', ico([(32,32,32,ico_bmp(b.resize((32,32)),32))]))
raw('a.cur', ico([(32,32,24,ico_bmp(im8,24))], kind=2))
