from python.service import get_order

def test_get_order() -> None:
    assert get_order("ord-7")["id"] == "ord-7"
