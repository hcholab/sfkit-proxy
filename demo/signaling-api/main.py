import asyncio
from typing import Dict

from quart import Quart, Websocket, request, websocket

app = Quart(__name__)

# using in-memory store for the demo
# TODO replace with database lookup in real implementation
studies = {
    "study_1": {"client_1", "client_2"},
    "study_2": {"client_2", "client_3"},
}

# may want to keep in-memory
connected_clients: Dict[str, Dict[str, Websocket]] = {}


@app.websocket("/api/ice")
async def handler():
    client_id = request.headers.get("oidc_claim_user_id")
    if not client_id:
        return "Unauthorized", 401

    # receive the first message containing the study ID
    msg = await websocket.receive_json()
    study_id = msg["studyId"]
    if not study_id:
        await websocket.send_json({"error": "Missing studyId"})
        return "Bad Request", 400

    # check study access
    if client_id not in studies.get(study_id, set()):
        await websocket.send_json({"error": "Client is not part of the study"})
        return "Forbidden", 403

    print(f"Client {client_id} connected for study {study_id}")

    # register the client as connected for the study
    connected_clients.setdefault(study_id, set())[client_id] = websocket

    # check if all participants in a study are connected
    # and initiate the ICE protocol for all
    if studies[study_id] == connected_clients[study_id].keys():
        await asyncio.gather(
            [
                ws.send_json({"type": "init", "clientId": cid})
                for cid, ws in connected_clients[study_id].items()
            ]
        )

    while True:
        # read the next message
        # (could be of type 'candidate' or 'credential')
        msg = await websocket.receive_json()
        print(f"Received: {msg}")

        # and broadcast it to all of the other participants
        await asyncio.gather(
            [
                ws.send_json(msg)
                for cid, ws in connected_clients[study_id].items()
                if cid != client_id
            ]
        )


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=8000)
