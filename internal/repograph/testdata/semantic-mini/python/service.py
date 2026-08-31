from fastapi import FastAPI
from python.repository import load_order

app = FastAPI()
AUDIT_TOPIC = "ORDER_AUDIT_TOPIC"

class OrderService:
    def fetch(self, order_id: str) -> dict:
        return load_order(order_id)

@app.get("/orders/{order_id}")
def get_order(order_id: str) -> dict:
    return OrderService().fetch(order_id)
