#!/usr/bin/env python3
import sys, json, threading
from queue import Queue, Empty

import rospy
from geometry_msgs.msg import Twist
from nav_msgs.msg import Odometry
from std_msgs.msg import String
from std_srvs.srv import Empty, SetBool, Trigger


class Ros1Bridge:
    def __init__(self):
        rospy.init_node("edge_ros_bridge", anonymous=True)
        rospy.loginfo("ROS1 bridge starting")

        self._pubs = {}
        self._subs = {}
        self._stdin_queue = Queue()

        self._stdin_thread = threading.Thread(target=self._read_stdin, daemon=True)
        self._stdin_thread.start()

        self._timer = rospy.Timer(rospy.Duration(0.05), self._process_stdin)
        rospy.loginfo("ROS1 bridge ready")

    def _read_stdin(self):
        for line in sys.stdin:
            line = line.strip()
            if not line:
                continue
            self._stdin_queue.put(line)

    def _process_stdin(self, event):
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

        pub = self._pubs.get(topic)
        if pub is None:
            cls = self._resolve_type(msg_type)
            if cls is None:
                self._send("error", error=f"unknown msg type: {msg_type}")
                return
            pub = rospy.Publisher(topic, cls, queue_size=10)
            self._pubs[topic] = (pub, cls)

        _, cls = pub
        ros_msg = cls()
        self._dict_to_ros(ros_msg, data)
        pub.publish(ros_msg)

    def _handle_subscribe(self, msg):
        topic = msg.get("topic", "")
        msg_type = msg.get("msg_type", "")
        if not topic or not msg_type:
            self._send("error", error="subscribe: topic and msg_type required")
            return
        if topic in self._subs:
            return

        cls = self._resolve_type(msg_type)
        if cls is None:
            self._send("error", error=f"unknown msg type: {msg_type}")
            return

        def cb(data, topic=topic):
            self._send("topic_data", topic=topic, data=self._ros_to_dict(data))

        self._subs[topic] = rospy.Subscriber(topic, cls, cb)
        rospy.loginfo(f"subscribed to {topic}")

    def _handle_unsubscribe(self, msg):
        topic = msg.get("topic", "")
        if topic in self._subs:
            self._subs.pop(topic).unregister()

    def _handle_service_call(self, msg):
        service = msg.get("service", "")
        srv_type = msg.get("msg_type", "")
        data = msg.get("data", {})

        if not service or not srv_type:
            self._send("error", error="service_call: service and msg_type required")
            return

        cls = self._resolve_type(srv_type)
        if cls is None:
            self._send("error", error=f"unknown srv type: {srv_type}")
            return

        try:
            rospy.wait_for_service(service, timeout=5.0)
            client = rospy.ServiceProxy(service, cls)
            req = cls._request_class() if hasattr(cls, "_request_class") else cls()
            if data:
                self._dict_to_ros(req, data)
            resp = client(req)
            self._send("service_call_result", name=service, success=True, data=self._ros_to_dict(resp))
        except Exception as e:
            self._send("service_call_result", name=service, success=False, data={"error": str(e)})

    def _handle_param_get(self, msg):
        name = msg.get("name", "")
        if not name:
            self._send("error", error="param_get: name required")
            return
        try:
            value = rospy.get_param(name)
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
            rospy.set_param(name, value)
            self._send("param_set_result", name=name, success=True, data={"value": str(value)})
        except Exception as e:
            self._send("param_set_result", name=name, success=False, data={"error": str(e)})

    def _send(self, typ, **kw):
        msg = {"type": typ}
        msg.update(kw)
        sys.stdout.write(json.dumps(msg) + "\n")
        sys.stdout.flush()

    def _resolve_type(self, type_str):
        known = {
            "geometry_msgs/Twist": Twist,
            "nav_msgs/Odometry": Odometry,
            "std_msgs/String": String,
        }
        cls = known.get(type_str)
        if cls:
            return cls
        parts = type_str.split("/")
        if len(parts) == 2:
            try:
                mod = __import__(f"{parts[0]}.msg", fromlist=[parts[1]])
                return getattr(mod, parts[1])
            except (ImportError, AttributeError):
                pass
        # also try srv
        known_srv = {
            "std_srvs/Empty": Empty,
            "std_srvs/SetBool": SetBool,
            "std_srvs/Trigger": Trigger,
        }
        cls = known_srv.get(type_str)
        if cls:
            return cls
        parts = type_str.split("/")
        if len(parts) == 2:
            try:
                mod = __import__(f"{parts[0]}.srv", fromlist=[parts[1]])
                return getattr(mod, parts[1])
            except (ImportError, AttributeError):
                pass
        return None

    def _dict_to_ros(self, msg, data):
        if data is None:
            return
        for key, value in data.items():
            if hasattr(msg, key):
                field = getattr(msg, key)
                if hasattr(field, "x") or hasattr(field, "linear") or hasattr(field, "position"):
                    self._dict_to_ros(field, value)
                elif isinstance(field, list):
                    setattr(msg, key, value)
                else:
                    setattr(msg, key, value)

    def _ros_to_dict(self, msg):
        fields = {}
        for slot in msg.__slots__ if hasattr(msg, "__slots__") else []:
            value = getattr(msg, slot)
            if hasattr(value, "__slots__"):
                fields[slot] = self._ros_to_dict(value)
            elif hasattr(value, "__len__") and not isinstance(value, (str, bytes)):
                try:
                    fields[slot] = [self._ros_to_dict(v) if hasattr(v, "__slots__") else v for v in value]
                except TypeError:
                    fields[slot] = str(value)
            else:
                fields[slot] = value
        return fields


def main():
    bridge = Ros1Bridge()
    rospy.spin()


if __name__ == "__main__":
    main()
