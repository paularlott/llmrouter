import router
import vars
import logging

l = logging.getLogger("router")

# A client that calls this router in-process; no endpoint, port or token needed
client = router.ai()

# Decision models reject input longer than their context (they never truncate) and slow down with length, so keep
# the start (usually the intent) and the end of long messages.
text = router.last_message()
if len(text) > 4000:
    text = text[:3000] + "\n...\n" + text[-1000:]

# One decision call scores the request in a single, non-streaming response.
# "choice" questions return the most likely option plus a probability per option.
result = client.decide(
    vars.decision_model,
    text,
    questions={
        "kind": {
            "type": "choice",
            "instructions": "Classify the request so it can be sent to a suitable model.",
            "criteria": {
                "simple": "Chit-chat, simple lookups, short answers, rewording",
                "code": "Writing, reviewing or debugging source code",
                "reasoning": "Multi-step reasoning, maths, planning or expert analysis",
            },
        },
    },
)

answer = result["answers"]["kind"]
kind = answer["choice"]
l.info("triage: " + kind + " confidence=" + str(answer["confidence"]))

# A flat distribution means the classifier was unsure: take the capable model
if answer["confidence"] < 0.3:
    kind = "reasoning"

models = {
    "simple": vars.simple_model,
    "code": vars.code_model,
    "reasoning": vars.reasoning_model,
}
router.set_model(models[kind])
