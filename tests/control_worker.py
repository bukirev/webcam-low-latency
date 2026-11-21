"""Test-only managed worker, without Tk, camera, audio or network video."""
import json
from pathlib import Path
import queue
import sys
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from panel_control import ManagedClient

client=ManagedClient.from_environment()
active=client.autostart
settings=dict(client.initial)
try:
    while True:
        client.latest_state=dict(active=active,role=client.role,settings=dict(settings),status='fixture')
        try: message=client.incoming.get(timeout=.05)
        except queue.Empty: continue
        command=message['command']
        if command=='shutdown':
            Path(client.directory,'closed.json').write_text(json.dumps(settings),encoding='utf-8')
            break
        if command=='apply':
            settings.update(message['settings'])
            active=active or message.get('start',True)
        client.outgoing.put(dict(type='ack',command=command))
finally:
    client.close()
