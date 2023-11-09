import asyncio
import os
from dataclasses import asdict, dataclass
from enum import Enum
from typing import Dict, List

import httpx
from quart import Quart, Websocket, abort, websocket

PID = int


class MessageType(Enum):
    CANDIDATE = "candidate"
    CREDENTIAL = "credential"
    CERTIFICATE = "certificate"
    ERROR = "error"


@dataclass
class Message:
    type: MessageType
    data: str = ""
    studyID: str = ""
    sourcePID: PID = -1
    targetPID: PID = -1

    async def send(self, ws=websocket):
        msg = asdict(self)
        for key, value in msg.items():
            if isinstance(value, Enum):
                msg[key] = value.value
        await ws.send_json(msg)

    @staticmethod
    async def receive():
        msg = await websocket.receive_json()
        print("Received", msg)
        msg["type"] = MessageType(msg["type"])
        return Message(**msg)


# using in-memory store for the demo
# TODO: replace with database lookup in real implementation
studies: Dict[str, List[str]] = {}

# in-memory stores for Websockets
study_barriers: Dict[str, asyncio.Barrier] = {}
study_parties: Dict[str, Dict[PID, Websocket]] = {}

PORT = os.getenv("PORT", "8000")
ORIGIN = os.getenv("ORIGIN", f"ws://host.docker.internal:{PORT}")
TERRA = os.getenv("TERRA", "y")

DEMO_USER_IDS = [s for s in os.getenv("DEMO_USER_IDS", "").split(",") if s]
DEMO = len(DEMO_USER_IDS) >= 2
if DEMO:
    studies = {
        "1": list(DEMO_USER_IDS),
        "2": list(DEMO_USER_IDS[1:]),
    }

AUTH_HEADER = "Authorization"
USER_ID_HEADER = "OIDC_CLAIM_USER_ID"
STUDY_ID_HEADER = "X-MPC-Study-ID"

app = Quart(__name__)


@app.websocket("/api/ice")
async def handler():
    # check origin
    origin = websocket.headers.get("Origin")
    if origin != ORIGIN:
        await Message(MessageType.ERROR, "Unexpected Origin header").send()
        abort(401)

    # retrieve user ID
    user_id = await _get_user_id()
    if not user_id:
        await Message(MessageType.ERROR, "Missing authentication").send()
        abort(401)

    # read study ID from the custom header
    study_id = websocket.headers.get(STUDY_ID_HEADER)
    if not study_id:
        await Message(MessageType.ERROR, f"Missing {STUDY_ID_HEADER} header").send()
        abort(400)

    # retrieve the study
    study = _get_study(study_id)

    # get PID for the user ID in this study
    # and  make sure they have access to the study
    pid = _get_pid(study, user_id)
    if pid < 0:
        await Message(
            MessageType.ERROR, f"User {user_id} is not in study {study_id}"
        ).send()
        abort(403)

    # get existing parties, if any, and check for a duplicate connection
    parties = study_parties.setdefault(study_id, {})
    if pid in parties:
        await Message(
            MessageType.ERROR,
            f"Party {pid} is already connected to study {study_id}",
        ).send()
        abort(409)

    try:
        # store the current websocket for the party
        parties[pid] = websocket._get_current_object()  # type: ignore
        print(f"Registered websocket for party {pid}")

        # using a study-specific barrier,
        # wait until all participants in a study are connected,
        # and then initiate the ICE protocol for it
        barrier = study_barriers.setdefault(study_id, asyncio.Barrier(len(study)))
        async with barrier:
            if pid == 0:
                print("All parties have connected:", ", ".join(str(k) for k in parties))

            while True:
                # read the next message and override its PID
                # (this prevents PID spoofing)
                msg = await Message.receive()
                msg.sourcePID = pid
                msg.studyID = study_id

                # and send it to the other party
                if msg.targetPID < 0:
                    await Message(
                        MessageType.ERROR, f"Missing target PID: {msg}"
                    ).send()
                    continue
                elif msg.targetPID not in parties or msg.targetPID == pid:
                    await Message(
                        MessageType.ERROR,
                        f"Unexpected target id {msg.targetPID}",
                    ).send()
                    continue
                else:
                    target_ws = parties[msg.targetPID]
                    await msg.send(target_ws)
    except Exception as e:
        print(f"Terminal connection error for party {pid} in study {study_id}: {e}")
    finally:
        del parties[pid]
        print(f"Party {pid} disconnected from study {study_id}")


async def _get_user_id():
    if DEMO:
        # when testing, get token subject from Google
        return await _get_subject_id()
    elif TERRA:
        # when running as a Terra service behind Apache proxy,
        # the proxy will take care of extracting JWT "sub" claim
        # into this header automatically
        return websocket.headers.get(USER_ID_HEADER)
    else:
        # TODO: if not on Terra, use existing auth
        return ""


async def _get_subject_id():
    async with httpx.AsyncClient() as client:
        res = await client.get(
            "https://www.googleapis.com/oauth2/v3/tokeninfo",
            headers={
                "Authorization": websocket.headers.get(AUTH_HEADER) or "",
            },
        )
        if res.is_error:
            body = str(res.read())
            await Message(
                MessageType.ERROR,
                f"Unable to fetch subject ID from Google: {res.status_code} {body}",
            ).send()
        else:
            body = res.json()
            sub = body.get("sub", body.get("azp", ""))
            return str(sub)


def _get_study(study_id: str):
    # retrieve the study
    if DEMO:
        return studies.setdefault(study_id, [])
    else:
        # TODO: lookup study from the database
        return []


def _get_pid(study: List[str], user_id: str) -> PID:
    if DEMO:
        # when testing, infer PID from a demo study, if present
        return study.index(user_id) if user_id in study else -1
    else:
        # TODO: lookup user ID -> PID in the real database object, if present
        return -1


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=int(PORT))
