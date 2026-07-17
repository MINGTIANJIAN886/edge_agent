#!/usr/bin/env python3
"""Receive model file via MQTT chunks and reassemble"""
import paho.mqtt.client as mqtt
import base64
import json

BROKER = "ca15b49bc8b442638f0cade1e45585ce.s1.eu.hivemq.cloud"
PORT = 8883
USER = "liyankun"
PASS = "liyankun152455A"
TOPIC = "edge/pi-001/filetransfer"

chunks = {}
total_chunks = 0
file_name = ""
file_size = 0

def on_message(client, userdata, msg):
    global chunks, total_chunks, file_name, file_size
    data = json.loads(msg.payload)
    idx = data["chunk"]
    total_chunks = data["total"]
    file_name = data["name"]
    file_size = data["size"]
    chunk_data = base64.b64decode(data["data"])
    chunks[idx] = chunk_data
    print(f"Received chunk {idx}/{total_chunks} ({len(chunk_data)} bytes)")

    if len(chunks) == total_chunks:
        out_path = f"/tmp/{file_name}"
        with open(out_path, "wb") as f:
            for i in range(1, total_chunks + 1):
                f.write(chunks[i])
        print(f"File saved to {out_path} ({file_size} bytes)")

        result_topic = "edge/pi-001/download/result"
        result = json.dumps({"id":"0","device_id":"pi-001","success":True,"path":out_path,"size":file_size})
        client.publish(result_topic, result, qos=1)
        print("Result published to MQTT")
        client.disconnect()

client = mqtt.Client()
client.tls_set()
client.username_pw_set(USER, PASS)
client.on_message = on_message
client.connect(BROKER, PORT)
client.subscribe(TOPIC, qos=1)
print(f"Listening on {TOPIC}...")
client.loop_forever()
