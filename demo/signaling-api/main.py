import asyncio
import os
from dataclasses import asdict, dataclass
from enum import Enum
from typing import Dict, Set

import httpx
from quart import Quart, Websocket, copy_current_websocket_context, websocket

app = Quart(__name__)

# using in-memory store for the demo
# TODO replace with database lookup in real implementation
studies: Dict[str, Set[str]] = {}

# may want to keep in-memory
study_barriers: Dict[str, asyncio.Barrier] = {}
study_clients: Dict[str, Dict[str, Websocket]] = {}

PORT = os.getenv("PORT", "8000")
ORIGIN = os.getenv("ORIGIN", f"ws://host.docker.internal:{PORT}")

DEMO_CLIENT_IDS = os.getenv("DEMO_CLIENT_IDS", "").split(",")
DEMO = len(DEMO_CLIENT_IDS) > 0
if DEMO:
    studies = {
        "1": set(DEMO_CLIENT_IDS[:2]),
        "2": set(DEMO_CLIENT_IDS[1:]),
    }


class MessageType(Enum):
    CONNECTED = "connected"
    ERROR = "error"


@dataclass
class Message:
    type: MessageType
    data: str = ""
    studyId: str = ""
    clientId: str = ""

    async def send(self, ws=websocket):
        msg = asdict(self)
        for key, value in msg.items():
            if isinstance(value, Enum):
                msg[key] = value.value

        await ws.send_json(msg)
        print(f"Sent", msg)


@app.websocket("/api/ice")
async def handler():
    # check origin
    if websocket.headers.get("Origin") != ORIGIN:
        return "Unauthorized", 401

    if DEMO:
        # when testing, get token subject from Google
        client_id = await get_subject_id()
    else:
        # when running as a Terra service behind Apache proxy,
        # the proxy will take care of extracting the claim automatically
        client_id = websocket.headers.get("oidc_claim_user_id")
    if not client_id:
        return "Unauthorized", 401

    # receive the first message containing the study ID
    msg = await websocket.receive_json()
    study_id = msg["studyId"]
    if not study_id:
        await Message(MessageType.ERROR, "Missing study ID").send()
        return "Bad Request", 400
    print(f"Received study ID {study_id} from client {client_id}")

    # get current study and its existing clients, if any
    # (TODO replace with a real database lookup)
    study = studies.setdefault(study_id, set())
    clients = study_clients.setdefault(study_id, {})

    # check study access
    if client_id not in study:
        await Message(
            MessageType.ERROR, f"Client {client_id} is not part of study {study_id}"
        ).send()
        return "Forbidden", 403

    # check for duplicate connection
    if client_id in clients:
        await Message(
            MessageType.ERROR,
            f"Client {client_id} is already connected to study {study_id}",
        ).send()
        return "Conflict", 409

    try:
        # store the current websocket send method for the client
        @copy_current_websocket_context
        async def ws_send(msg: dict):
            await websocket.send_json(msg)

        clients[client_id] = ws_send
        print(f"Registered websocket for client {client_id}")

        # using a study-specific barrier,
        # wait until all participants in a study are connected,
        # and then initiate the ICE protocol for it
        async with study_barriers.setdefault(study_id, asyncio.Barrier(len(study))):
            await Message(MessageType.CONNECTED, clientId=client_id).send()
            print("All clients have connected:", clients.keys())

            while True:
                # read the next message and override its client ID
                # (could be of type 'candidate' or 'credential')
                msg = await websocket.receive_json()
                msg["clientId"] = client_id
                print(f"Received: {msg}")

                # and broadcast it to all of the other participants
                await asyncio.gather(
                    *(send(msg) for cid, send in clients.items() if cid != client_id)
                )
                print(f"Broadcast: {msg}")
    except Exception as e:
        print(f"Terminal error in client {client_id} connection: {e}")
    finally:
        del clients[client_id]


async def get_subject_id():
    async with httpx.AsyncClient() as client:
        res = await client.get(
            "https://www.googleapis.com/oauth2/v3/tokeninfo",
            headers={
                "Authorization": websocket.headers.get("authorization"),
            },
        )
        if res.is_error:
            await Message(
                MessageType.ERROR,
                f"Unable to fetch subject ID from Google: {res.status_code} {str(res.read())}",
            ).send()
        else:
            return res.json()["sub"]


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=int(PORT))
