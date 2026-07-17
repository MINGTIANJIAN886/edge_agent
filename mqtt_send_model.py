#!/usr/bin/env python3
"""Send model file via MQTT in chunks"""
import paho.mqtt.client as mqtt
import base64
import time
import sys
import os

BROKER = "ca15b49bc8b442638f0cade1e45585ce.s1.eu.hivemq.cloud"
PORT = 8883
USER = "liyankun"
PASS = "liyankun152455A"
CHUNK_SIZE = 512 * 1024  # 512KB chunks

model_path = sys.argv[1] if len(sys.argv) > 1 else "/home/l22/桌面/yolo11/best_ncnn_model/model.ncnn.bin"
topic = sys.argv[2] if len(sys.argv) > 2 else "edge/pi-001/filetransfer"
file_name = os.path.basename(model_path)
file_size = os.path.getsize(model_path)
total_chunks = (file_size + CHUNK_SIZE - 1) // CHUNK_SIZE

client = mqtt.Client()
client.tls_set()
client.username_pw_set(USER, PASS)
client.connect(BROKER, PORT)
client.loop_start()

with open(model_path, "rb") as f:
    for i in range(total_chunks):
        chunk = f.read(CHUNK_SIZE)
        b64_data = base64.b64encode(chunk).decode()
        payload = '{"id":"%d","name":"%s","chunk":%d,"total":%d,"data":"%s","size":%d}' % (
            i, file_name, i + 1, total_chunks, b64_data, file_size)
        client.publish(topic, payload, qos=1)
        print(f"Sent chunk {i+1}/{total_chunks} ({len(chunk)} bytes)")
        time.sleep(0.1)

time.sleep(1)
client.disconnect()
print("Done!")
