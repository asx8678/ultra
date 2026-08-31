use crate::repository::load_order;

pub const AUDIT_TOPIC: &str = "ORDER_AUDIT_TOPIC";

pub trait OrderRepository {
    fn load_order(&self, id: &str) -> String;
}

pub fn install_order_routes(router: Router) -> Router {
    router.route("/orders/:id", get(get_order))
}

pub fn get_order(id: &str) -> String {
    load_order(id)
}

mod repository;

pub struct Router;

impl Router {
    pub fn route<T>(self, _path: &str, _handler: T) -> Self {
        self
    }
}

fn get<T>(handler: T) -> T {
    handler
}
