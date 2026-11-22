import ctypes, json, sys
hb = ctypes.CDLL('libharfbuzz.so.0')
class Info(ctypes.Structure):
    _fields_=[('codepoint',ctypes.c_uint32),('mask',ctypes.c_uint32),('cluster',ctypes.c_uint32),('v1',ctypes.c_uint32),('v2',ctypes.c_uint32)]
class Pos(ctypes.Structure):
    _fields_=[('xa',ctypes.c_int32),('ya',ctypes.c_int32),('xo',ctypes.c_int32),('yo',ctypes.c_int32),('v',ctypes.c_uint32)]
class Feat(ctypes.Structure):
    _fields_=[('tag',ctypes.c_uint32),('value',ctypes.c_uint32),('start',ctypes.c_uint),('end',ctypes.c_uint)]
hb.hb_blob_create_from_file.restype=ctypes.c_void_p
hb.hb_face_create.restype=ctypes.c_void_p
hb.hb_face_create.argtypes=[ctypes.c_void_p,ctypes.c_uint]
hb.hb_font_create.restype=ctypes.c_void_p
hb.hb_font_create.argtypes=[ctypes.c_void_p]
hb.hb_buffer_create.restype=ctypes.c_void_p
hb.hb_buffer_add_utf32.argtypes=[ctypes.c_void_p,ctypes.c_void_p,ctypes.c_int,ctypes.c_uint,ctypes.c_int]
hb.hb_buffer_guess_segment_properties.argtypes=[ctypes.c_void_p]
hb.hb_buffer_set_direction.argtypes=[ctypes.c_void_p,ctypes.c_int]
hb.hb_buffer_set_script.argtypes=[ctypes.c_void_p,ctypes.c_uint32]
hb.hb_buffer_set_language.argtypes=[ctypes.c_void_p,ctypes.c_void_p]
hb.hb_language_from_string.restype=ctypes.c_void_p
hb.hb_language_from_string.argtypes=[ctypes.c_char_p,ctypes.c_int]
hb.hb_shape.argtypes=[ctypes.c_void_p,ctypes.c_void_p,ctypes.c_void_p,ctypes.c_uint]
hb.hb_buffer_get_glyph_infos.restype=ctypes.POINTER(Info)
hb.hb_buffer_get_glyph_infos.argtypes=[ctypes.c_void_p,ctypes.POINTER(ctypes.c_uint)]
hb.hb_buffer_get_glyph_positions.restype=ctypes.POINTER(Pos)
hb.hb_buffer_get_glyph_positions.argtypes=[ctypes.c_void_p,ctypes.POINTER(ctypes.c_uint)]
hb.hb_buffer_destroy.argtypes=[ctypes.c_void_p]
hb.hb_feature_from_string.argtypes=[ctypes.c_char_p,ctypes.c_int,ctypes.POINTER(Feat)]
hb.hb_buffer_set_cluster_level.argtypes=[ctypes.c_void_p,ctypes.c_int]
def tag(s): return (ord(s[0])<<24)|(ord(s[1])<<16)|(ord(s[2])<<8)|ord(s[3])
cases=json.load(open(sys.argv[1]))
faces={}
out=[]
for c in cases:
    key=(c['font'],c.get('index',0))
    if key not in faces:
        blob=hb.hb_blob_create_from_file(c['font'].encode())
        face=hb.hb_face_create(blob,c.get('index',0))
        faces[key]=hb.hb_font_create(face)
    font=faces[key]
    buf=hb.hb_buffer_create()
    cps=[ord(ch) for ch in c['text']]
    arr=(ctypes.c_uint32*len(cps))(*cps)
    hb.hb_buffer_add_utf32(buf,arr,len(cps),0,len(cps))
    hb.hb_buffer_set_direction(buf, 5 if c.get('rtl') else 4)
    hb.hb_buffer_set_script(buf, tag(c.get('script','Latn')))
    if c.get('lang'):
        hb.hb_buffer_set_language(buf, hb.hb_language_from_string(c['lang'].encode(),-1))
    feats=(Feat*len(c.get('features',[])))()
    for i,f in enumerate(c.get('features',[])):
        hb.hb_feature_from_string(f.encode(),-1,ctypes.byref(feats[i]))
    hb.hb_shape(font,buf,feats,len(c.get('features',[])))
    n=ctypes.c_uint()
    inf=hb.hb_buffer_get_glyph_infos(buf,ctypes.byref(n))
    pos=hb.hb_buffer_get_glyph_positions(buf,ctypes.byref(n))
    gl=[[inf[i].codepoint,inf[i].cluster,pos[i].xa,pos[i].xo,pos[i].yo] for i in range(n.value)]
    if c.get('rtl'): gl=gl[::-1]
    out.append(gl)
    hb.hb_buffer_destroy(buf)
json.dump(out,open(sys.argv[2],'w'))
