import sys
ref=dict(l.split(' ',1) for l in open('/tmp/imgt/ref.txt').read().splitlines())
go=dict(l.split(' ',1) for l in open('/tmp/imgt/go.txt').read().splitlines())
ok=0
for k in sorted(ref):
    if ref[k]==go.get(k): ok+=1
    else: print(f'{k:28} pillow={ref[k][:60]:60} go={go.get(k,"-")[:60]}')
print(ok,'/',len(ref),'match')
