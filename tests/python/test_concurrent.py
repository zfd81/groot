#!/usr/bin/env python3
"""
Test concurrent conflict - send first request, query status, send second request
All while first request is still running (Mock LLM takes 10s)
"""

import requests
import time
import threading

BASE_URL = "http://localhost:8080"
API_KEY = "test-api-key-2026"

# Global variables
session_id = None
first_response_body = None

def send_first_request():
    """Send first request and capture session_id from headers"""
    global session_id, first_response_body

    headers = {
        "Content-Type": "application/json",
        "X-API-Key": API_KEY
    }

    response = requests.post(
        f"{BASE_URL}/chat",
        headers=headers,
        json={"instruction": "test concurrent"},
        stream=True  # Don't wait for body
    )

    # Get session_id from response headers (available immediately)
    session_id = response.headers.get("X-Session-Id")
    print(f"[First Request] Session ID: {session_id}")

    # Read the SSE body (will take ~10s due to Mock LLM)
    first_response_body = response.text

    print(f"[First Request] Completed")

def check_status():
    """Check status of the session"""
    headers = {
        "X-API-Key": API_KEY
    }

    response = requests.get(
        f"{BASE_URL}/chat/status/{session_id}",
        headers=headers
    )

    data = response.json()
    print(f"[Status Check] Response: {data}")
    return data

def send_second_request():
    """Send second request with same session_id"""
    headers = {
        "Content-Type": "application/json",
        "X-API-Key": API_KEY,
        "X-Session-ID": session_id
    }

    response = requests.post(
        f"{BASE_URL}/chat",
        headers=headers,
        json={"instruction": "second request"}
    )

    print(f"[Second Request] Status code: {response.status_code}")
    print(f"[Second Request] Response: {response.text[:200]}")
    return response.status_code

if __name__ == "__main__":
    # Start first request in background thread
    first_thread = threading.Thread(target=send_first_request)
    first_thread.start()

    # Wait for session_id to be available
    while session_id is None:
        time.sleep(0.1)

    print("\n=== Testing concurrent conflict ===")
    print(f"Session ID obtained: {session_id}")

    # Immediately check status (should be running)
    print("\n[1] Checking status immediately...")
    status_data = check_status()

    # Send second request immediately (should return 409)
    print("\n[2] Sending second request...")
    second_status = send_second_request()

    # Wait for first request to complete
    print("\n[3] Waiting for first request to complete...")
    first_thread.join(timeout=15)

    # Final status check
    print("\n[4] Final status check...")
    final_status = check_status()

    # Print results
    print("\n=== Results ===")
    if second_status == 409:
        print("✅ Concurrent conflict correctly returned 409")
    else:
        print(f"❌ Expected 409, got {second_status}")

    if status_data.get("chat") and status_data["chat"].get("status") == "running":
        print("✅ Status correctly showed running")
    else:
        print("❌ Status did not show running")