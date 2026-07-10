import router
import logging

l = logging.getLogger("router")

l.info("running auto router")

# Route every request to a fixed model. Replace with your own logic, e.g.
#   if "image_url" in router.message_content_types():
#       router.set_model("gpt-4o")
router.set_model("smollm2-360m-instruct")

l.info("completed router")
