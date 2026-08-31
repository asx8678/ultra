import { Router } from "express";
import { auditTopic } from "./settings";

export class OrderController {
  getOrder(id: string): string {
    publishAudit(auditTopic, id);
    return id;
  }
}

export function installOrderRoutes(router: Router, controller: OrderController): void {
  router.get("/orders/:id", (request) => controller.getOrder(request.params.id));
}

function publishAudit(topic: string, id: string): void {
  console.log(topic, id);
}
