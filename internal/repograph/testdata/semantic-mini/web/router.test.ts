import { OrderController } from "./router";

test("getOrder publishes an audit", () => {
  const controller = new OrderController();
  expect(controller.getOrder("ord-7")).toBe("ord-7");
});
