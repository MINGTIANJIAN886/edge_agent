import rclpy
from rclpy.node import Node
from geometry_msgs.msg import Twist
import paho.mqtt.client as mqtt
import json
import os
import signal
import threading

BROKER = "ca15b49bc8b442638f0cade1e45585ce.s1.eu.hivemq.cloud"
PORT = 8883
CAFILE = "/etc/ssl/certs/ca-certificates.crt"
USER = "liyankun"
PASS = "liyankun152455A"
TOPIC = "edge/pi-001/car/command"
RESULT_TOPIC = "edge/pi-001/car/result"

DIRECTION_MAP = {
    "forward":  (1.0, 0.0),
    "backward": (-1.0, 0.0),
    "left":     (0.0, 1.0),
    "right":    (0.0, -1.0),
    "stop":     (0.0, 0.0),
    "rotate_l": (0.0, 1.0),
    "rotate_r": (0.0, -1.0),
}

class CarBridge(Node):
    def __init__(self):
        super().__init__("car_bridge")
        self.pub = self.create_publisher(Twist, "/cmd_vel", 10)
        self.max_linear = 1.0
        self.max_angular = 2.0
        self._stop_timer = None
        self.get_logger().info("Car bridge starting...")

        self.mqtt_client = mqtt.Client()
        if os.path.exists(CAFILE):
            self.mqtt_client.tls_set(CAFILE)
        self.mqtt_client.username_pw_set(USER, PASS)
        self.mqtt_client.on_connect = self.on_connect
        self.mqtt_client.on_message = self.on_message

        try:
            self.mqtt_client.connect_async(BROKER, PORT, 60)
            self.mqtt_client.loop_start()
            self.get_logger().info(f"MQTT connecting to {BROKER}:{PORT}")
        except Exception as e:
            self.get_logger().error(f"MQTT connection failed: {e}")

        signal.signal(signal.SIGINT, self.shutdown)
        signal.signal(signal.SIGTERM, self.shutdown)

    def shutdown(self, *args):
        self.get_logger().info("Shutting down...")
        self.stop()
        self.mqtt_client.loop_stop()
        self.mqtt_client.disconnect()
        rclpy.shutdown()

    def on_connect(self, client, userdata, flags, rc):
        self.get_logger().info(f"MQTT connected, subscribing to {TOPIC}")
        client.subscribe(TOPIC, qos=1)

    def publish_twist(self, linear_x, angular_z):
        twist = Twist()
        twist.linear.x = float(linear_x)
        twist.angular.z = float(angular_z)
        self.pub.publish(twist)
        self.get_logger().info(f"Published: linear={twist.linear.x:.2f} angular={twist.angular.z:.2f}")

    def stop(self):
        self.publish_twist(0.0, 0.0)
        if self._stop_timer:
            self._stop_timer.cancel()
            self._stop_timer = None

    def schedule_stop(self, duration_ms):
        if self._stop_timer:
            self._stop_timer.cancel()
        self._stop_timer = threading.Timer(duration_ms / 1000.0, self.stop)
        self._stop_timer.daemon = True
        self._stop_timer.start()

    def publish_result(self, success, message, data=None):
        result = {"success": success, "message": message}
        if data:
            result["data"] = data
        self.mqtt_client.publish(RESULT_TOPIC, json.dumps(result), qos=1)

    def on_message(self, client, userdata, msg):
        try:
            data = json.loads(msg.payload)
            direction = data.get("direction", "")
            speed = float(data.get("speed", 0.2))
            linear_x = data.get("linear_x")
            angular_z = data.get("angular_z")
            duration_ms = data.get("duration_ms", 0)

            if direction in ("stop",):
                self.stop()
                self.publish_result(True, "stopped")
                return

            if linear_x is not None and angular_z is not None:
                lx = float(linear_x) * self.max_linear
                az = float(angular_z) * self.max_angular
                self.publish_twist(lx, az)
                if duration_ms > 0:
                    self.schedule_stop(duration_ms)
                self.publish_result(True, f"velocity: lx={lx:.2f} az={az:.2f}", {"linear_x": lx, "angular_z": az})
                return

            if direction in DIRECTION_MAP:
                lx, az = DIRECTION_MAP[direction]
                if direction in ("rotate_l", "rotate_r"):
                    lx = 0.0
                    az = az * speed * self.max_angular
                else:
                    lx = lx * speed * self.max_linear
                    az = az * speed * self.max_angular
                self.publish_twist(lx, az)
                if duration_ms > 0:
                    self.schedule_stop(duration_ms)
                self.publish_result(True, f"{direction} speed={speed}", {"linear_x": lx, "angular_z": az})
                return

            if direction == "curve":
                linear_s = float(data.get("linear", speed)) * self.max_linear
                angular_s = float(data.get("angular", speed * 0.5)) * self.max_angular
                self.publish_twist(linear_s, angular_s)
                if duration_ms > 0:
                    self.schedule_stop(duration_ms)
                self.publish_result(True, f"curve: lx={linear_s:.2f} az={angular_s:.2f}")
                return

            self.publish_result(False, f"unknown direction: {direction}")
        except Exception as e:
            self.get_logger().error(f"Error: {e}")
            self.publish_result(False, str(e))

def main():
    rclpy.init()
    node = CarBridge()
    try:
        rclpy.spin(node)
    except KeyboardInterrupt:
        pass
    finally:
        node.stop()
        node.mqtt_client.loop_stop()
        node.mqtt_client.disconnect()
        node.destroy_node()
        rclpy.shutdown()

if __name__ == "__main__":
    main()
