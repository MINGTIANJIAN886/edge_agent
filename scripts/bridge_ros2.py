#!/usr/bin/env python3
import sys, json, threading, traceback
from queue import Queue, Empty

import rclpy
from rclpy.node import Node
from geometry_msgs.msg import Twist
from nav_msgs.msg import Odometry
from std_msgs.msg import String
from std_srvs.srv import Empty, SetBool, Trigger


class Ros2Bridge(Node):
    def __init__(self):
        super().__init__("edge_ros_bridge")
        self.get_logger().info("ROS2 bridge starting")

        self._pub_cache = {}
        self._sub_cache = {}
        self._stdin_queue = Queue()

        self._stdin_thread = threading.Thread(target=self._read_stdin, daemon=True)
        self._stdin_thread.start()

        self._timer = self.create_timer(0.05, self._process_stdin)
        self.get_logger().info("ROS2 bridge ready")

    def _read_stdin(self):
        for line in sys.stdin:
            line = line.strip()
            if not line:
                continue
            self._stdin_queue.put(line)

    def _process_stdin(self):
        try:
            while True:
                line = self._stdin_queue.get_nowait()
                self._handle_input(line)
        except Empty:
            pass

    def _handle_input(self, line):
        try:
            msg = json.loads(line)
        except json.JSONDecodeError as e:
            self._send("error", error=f"invalid JSON: {e}")
            return

        cmd = msg.get("cmd")
        if cmd == "publish":
            self._handle_publish(msg)
        elif cmd == "subscribe":
            self._handle_subscribe(msg)
        elif cmd == "unsubscribe":
            self._handle_unsubscribe(msg)
        elif cmd == "service_call":
            self._handle_service_call(msg)
        elif cmd == "param_get":
            self._handle_param_get(msg)
        elif cmd == "param_set":
            self._handle_param_set(msg)
        else:
            self._send("error", error=f"unknown cmd: {cmd}")

    def _handle_publish(self, msg):
        topic = msg.get("topic", "")
        msg_type = msg.get("msg_type", "")
        data = msg.get("data", {})

        if not topic or not msg_type:
            self._send("error", error="publish: topic and msg_type required")
            return

        pub = self._pub_cache.get(topic)
        if pub is None:
            cls = self._resolve_type(msg_type)
            if cls is None:
                self._send("error", error=f"unknown msg type: {msg_type}")
                return
            pub = self.create_publisher(cls, topic, 10)
            self._pub_cache[topic] = (pub, cls)

        _, cls = pub
        ros_msg = cls()
        self._dict_to_ros(ros_msg, data)
        pub.publish(ros_msg)
        self.get_logger().debug(f"published {topic}")

    def _handle_subscribe(self, msg):
        topic = msg.get("topic", "")
        msg_type = msg.get("msg_type", "")
        if not topic or not msg_type:
            self._send("error", error="subscribe: topic and msg_type required")
            return
        if topic in self._sub_cache:
            return

        cls = self._resolve_type(msg_type)
        if cls is None:
            self._send("error", error=f"unknown msg type: {msg_type}")
            return

        def cb(data, topic=topic):
            d = self._ros_to_dict(data)
            self._send("topic_data", topic=topic, data=d)

        self._sub_cache[topic] = self.create_subscription(cls, topic, cb, 10)
        self.get_logger().info(f"subscribed to {topic}")

    def _handle_unsubscribe(self, msg):
        topic = msg.get("topic", "")
        if topic in self._sub_cache:
            self.destroy_subscription(self._sub_cache.pop(topic))

    def _handle_service_call(self, msg):
        service = msg.get("service", "")
        srv_type = msg.get("msg_type", "")
        data = msg.get("data", {})

        if not service or not srv_type:
            self._send("error", error="service_call: service and msg_type required")
            return

        cls = self._resolve_type(srv_type, srv=True)
        if cls is None:
            self._send("error", error=f"unknown srv type: {srv_type}")
            return

        client = self.create_client(cls, service)
        if not client.wait_for_service(timeout_sec=5.0):
            self._send("service_call_result", name=service, success=False, data={"error": "service not available"})
            return

        req = cls.Request()
        if data:
            self._dict_to_ros(req, data)

        future = client.call_async(req)
        future.add_done_callback(lambda f, s=service: self._on_service_done(f, s))

    def _on_service_done(self, future, service):
        try:
            response = future.result()
            self._send("service_call_result", name=service, success=True, data=self._ros_to_dict(response))
        except Exception as e:
            self._send("service_call_result", name=service, success=False, data={"error": str(e)})

    def _handle_param_get(self, msg):
        name = msg.get("name", "")
        if not name:
            self._send("error", error="param_get: name required")
            return
        try:
            value = self.get_parameter(name).value
            self._send("param_get_result", name=name, success=True, data={"value": str(value)})
        except Exception as e:
            self._send("param_get_result", name=name, success=False, data={"error": str(e)})

    def _handle_param_set(self, msg):
        name = msg.get("name", "")
        value = msg.get("value")
        if not name:
            self._send("error", error="param_set: name required")
            return
        try:
            from rclpy.parameter import Parameter
            self.set_parameters([Parameter(name, value=value)])
            self._send("param_set_result", name=name, success=True, data={"value": str(value)})
        except Exception as e:
            self._send("param_set_result", name=name, success=False, data={"error": str(e)})

    def _send(self, typ, **kw):
        msg = {"type": typ}
        msg.update(kw)
        sys.stdout.write(json.dumps(msg) + "\n")
        sys.stdout.flush()

    def _resolve_type(self, type_str, srv=False):
        known = {
            "geometry_msgs/msg/Twist": Twist,
            "geometry_msgs/Twist": Twist,
            "nav_msgs/msg/Odometry": Odometry,
            "nav_msgs/Odometry": Odometry,
            "std_msgs/msg/String": String,
        }
        if not srv:
            cls = known.get(type_str)
            if cls:
                return cls
            parts = type_str.split("/")
            if len(parts) == 3 and parts[1] == "msg":
                try:
                    mod = __import__(f"{parts[0]}.msg", fromlist=[parts[2]])
                    return getattr(mod, parts[2])
                except (ImportError, AttributeError):
                    pass
        else:
            known_srv = {
                "std_srvs/srv/Empty": Empty,
                "std_srvs/srv/SetBool": SetBool,
                "std_srvs/srv/Trigger": Trigger,
            }
            cls = known_srv.get(type_str)
            if cls:
                return cls
            parts = type_str.split("/")
            if len(parts) == 3 and parts[1] == "srv":
                try:
                    mod = __import__(f"{parts[0]}.srv", fromlist=[parts[2]])
                    return getattr(mod, parts[2])
                except (ImportError, AttributeError):
                    pass
        return None

    def _dict_to_ros(self, msg, data):
        if data is None:
            return
        for key, value in data.items():
            if hasattr(msg, key):
                field = getattr(msg, key)
                if hasattr(field, "get_fields_and_field_types"):
                    self._dict_to_ros(field, value)
                elif isinstance(field, list):
                    setattr(msg, key, value)
                else:
                    setattr(msg, key, value)

    def _ros_to_dict(self, msg):
        fields = {}
        for field in msg.get_fields_and_field_types().keys():
            value = getattr(msg, field)
            if hasattr(value, "get_fields_and_field_types"):
                fields[field] = self._ros_to_dict(value)
            elif hasattr(value, "__len__") and not isinstance(value, (str, bytes)):
                try:
                    fields[field] = [self._ros_to_dict(v) if hasattr(v, "get_fields_and_field_types") else v for v in value]
                except TypeError:
                    fields[field] = str(value)
            else:
                fields[field] = value
        return fields


def main():
    import os
    os.makedirs("/tmp/ros/log", exist_ok=True)
    os.environ["ROS_HOME"] = "/tmp/ros"
    rclpy.init()
    bridge = Ros2Bridge()
    try:
        rclpy.spin(bridge)
    except KeyboardInterrupt:
        pass
    finally:
        bridge.destroy_node()
        rclpy.shutdown()


if __name__ == "__main__":
    main()
